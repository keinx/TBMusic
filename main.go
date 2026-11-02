// TBMusic 音乐网站:Go 后端(采集脚本宿主 + 内置音源) + Vue 前端
package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"

	"tbmusic/internal/cache"
	"tbmusic/internal/config"
	"tbmusic/internal/logging"
	"tbmusic/internal/lxsource"
	"tbmusic/internal/server"
	"tbmusic/internal/store"
)

// maxLogBytes 日志文件硬上限:超出后自动只保留最新的约一半
const maxLogBytes = 10 << 20

func bcryptHash(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

//go:embed all:web/dist
var webDist embed.FS

const configPath = "config.yaml"

func main() {
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Println("加载配置失败:", err)
		os.Exit(1)
	}
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		cfg.Save(configPath) // 首次运行生成配置文件
	}

	// 日志统一落盘到数据库同目录,单文件上限 10MB(超出自动截断,不再无限增长)
	logPath := filepath.Join(filepath.Dir(cfg.Database.Path), "tbmusic.log")
	logFile, logErr := logging.NewWriter(logPath, maxLogBytes)
	if logErr == nil {
		log.SetOutput(logFile)
		defer logFile.Close()
	} else {
		log.Printf("[日志] 打开日志文件 %s 失败,改为输出到控制台: %v", logPath, logErr)
	}

	st, err := store.Open(cfg.Database.Path)
	if err != nil {
		fmt.Println("打开数据库失败:", err)
		os.Exit(1)
	}

	st.SetSessionDays(cfg.Session.Days)

	// 确保超级管理员 admin 存在(默认密码 123456,登录后请修改)
	adminHash, _ := bcryptHash("123456")
	if err := st.EnsureAdmin(adminHash); err != nil {
		fmt.Println("初始化管理员失败:", err)
		os.Exit(1)
	}

	lx := lxsource.NewPool()
	srv := server.New(st, cfg, configPath, lx)
	cacheDir := cfg.Cache.Dir
	if cacheDir == "" {
		cacheDir = filepath.Join(filepath.Dir(cfg.Database.Path), "cache")
	}
	dataCache, err := cache.Open(cacheDir, cfg.GetCacheSizeMB()<<20)
	if err != nil {
		fmt.Println("打开缓存失败:", err)
		st.Close()
		os.Exit(1)
	}
	srv.Cache = dataCache
	if dist, err := fs.Sub(webDist, "web/dist"); err == nil {
		srv.StaticFS = dist
	}

	// 加载采集接口脚本(异步,不阻塞启动);第一个为主音源
	if scripts := cfg.ActiveScripts(); len(scripts) > 0 {
		fmt.Printf("[采集接口] 正在加载 %d 个脚本,主音源: %s\n", len(scripts), scripts[0].Name)
		go srv.ReloadScripts()
	} else {
		fmt.Println("[采集接口] 未配置,请在 config.yaml 中填写 source.scripts")
	}

	httpSrv := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      0, // 音频长连接由 handleStream 分别设置上游读空闲与下游写超时
		IdleTimeout:       120 * time.Second,
	}

	// 先 Listen 再打印地址,保证打印出来的端口就是真实监听的端口
	ln, err := net.Listen("tcp", cfg.Server.Addr)
	if err != nil {
		fmt.Println("监听失败:", err)
		lx.Close()
		st.Close()
		os.Exit(1)
	}

	fmt.Println("========================================")
	fmt.Println("  TBMusic 音乐网站已启动")
	listenAddrs(ln.Addr().String())
	if logErr == nil {
		fmt.Println("  日志:   " + logPath + "(上限 10MB,超出自动截断)")
	}
	fmt.Println("========================================")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() { serveErr <- httpSrv.Serve(ln) }()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Println("服务异常退出:", err)
		}
	case <-ctx.Done():
		fmt.Println("\n收到退出信号,正在关闭...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			fmt.Println("优雅关闭超时,强制退出:", err)
			_ = httpSrv.Close()
		}
	}

	// 收尾:停脚本事件循环(释放 goroutine 与 VM),关闭数据库连接
	lx.Close()
	if err := st.Close(); err != nil {
		fmt.Println("关闭数据库失败:", err)
	}
	fmt.Println("已安全退出")
}

// listenAddrs 打印本机与局域网访问地址,方便手机浏览器打开
func listenAddrs(addr string) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		fmt.Println("  地址:", "http://127.0.0.1"+resolvePort(addr))
		return
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		fmt.Println("  本机:   http://127.0.0.1:" + port)
		if ips := lanIPs(); len(ips) > 0 {
			for _, ip := range ips {
				fmt.Println("  局域网: http://" + ip + ":" + port + "  (手机浏览器可访问)")
			}
		}
	} else {
		fmt.Println("  地址:   http://" + addr)
	}
}

func resolvePort(addr string) string {
	if len(addr) > 0 && addr[0] == ':' {
		return addr
	}
	return ":8080"
}

func lanIPs() []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok {
				ip4 := ipnet.IP.To4()
				if ip4 != nil {
					out = append(out, ip4.String())
				}
			}
		}
	}
	return out
}
