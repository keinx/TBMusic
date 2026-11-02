package lxsource

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/console"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// bootstrap 初始化 globalThis.lx 运行环境(LX 自定义音源协议 v2.0.0)
const bootstrap = `
(function(){
  globalThis.lx = {
    EVENT_NAMES: { request: 'request', inited: 'inited', updateAlert: 'updateAlert' },
    version: '2.0.0',
    VERSION: '2.0.0',
    env: 'desktop',
    currentScriptInfo: globalThis.__scriptInfo,
    on: (ev, handler) => globalThis.__lxOn(String(ev), handler),
    send: (ev, data) => globalThis.__lxSend(String(ev), data),
    request: (url, options, callback) => globalThis.__lxRequest(url, options, callback),
    utils: {
      buffer: {
        from: (v, enc) => __lxBufferFrom(v, enc),
        bufToString: (buf, enc) => __lxBufToString(buf, enc),
      },
      crypto: {
        md5: (s) => __lxMd5(String(s)),
        randomBytes: (n) => __lxRandomBytes(n | 0),
        aesEncrypt: (buf, mode, key, iv) => __lxAesEncrypt(__bytesOf(buf), String(mode), __bytesOf(key), __bytesOf(iv)),
        rsaEncrypt: (buf, key) => __lxRsaEncrypt(__bytesOf(buf), String(key)),
      },
      zlib: {
        inflate: (buf) => Promise.resolve().then(() => __lxInflate(__bytesOf(buf))),
        deflate: (buf) => Promise.resolve().then(() => __lxDeflate(__bytesOf(buf))),
      },
    },
  };
  globalThis.__bytesOf = (v) => {
    if (v == null) return null;
    if (typeof v === 'string') return __goBytes(v, 'utf8');
    if (v instanceof ArrayBuffer) v = new Uint8Array(v);
    if (Array.isArray(v)) { const o = []; for (const x of v) o.push(Number(x) & 0xff); return __goBytesArr(o); }
    if (v && typeof v.length === 'number') { const o = []; for (let i = 0; i < v.length; i++) o.push(Number(v[i]) & 0xff); return __goBytesArr(o); }
    if (v && typeof v.byteLength === 'number' && typeof v.getUint8 === 'function') { const o = []; for (let i = 0; i < v.byteLength; i++) o.push(v.getUint8(i)); return __goBytesArr(o); }
    return null;
  };
})();
`

// hostUA 提供给脚本的浏览器 UA
const hostUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// scriptPrinter 把脚本 console 输出转写进应用日志(受 10MB 上限约束)
type scriptPrinter struct{ name string }

func (p scriptPrinter) Log(s string)   { log.Printf("[%s] %s", p.name, s) }
func (p scriptPrinter) Warn(s string)  { log.Printf("[%s][warn] %s", p.name, s) }
func (p scriptPrinter) Error(s string) { log.Printf("[%s][error] %s", p.name, s) }

// installConsole 用自定义 printer 重建 console 对象;
// 失败时保留 goja_nodejs 默认实现,不影响脚本运行。
func (m *Manager) installConsole(vm *goja.Runtime) {
	module := vm.NewObject()
	exports := vm.NewObject()
	module.Set("exports", exports)
	func() {
		defer func() { _ = recover() }()
		console.RequireWithPrinter(scriptPrinter{name: m.Name})(vm, module)
		vm.Set("console", exports)
	}()
}

// registerEnv 在 goja 运行时上安装 lx 桥接函数
func (m *Manager) registerEnv(vm *goja.Runtime) {
	m.bindScopedTimers(vm)
	// console 默认由 goja_nodejs 直接写 stdout(log.New(os.Stdout)),会绕过应用日志,
	// 导致脚本输出不受大小上限约束。这里换成写应用日志的 printer。
	m.installConsole(vm)

	vm.Set("__scriptInfo", scriptInfoObj(vm, m.GetInfo()))

	vm.Set("__lxOn", func(call goja.FunctionCall) goja.Value {
		ev := call.Argument(0).String()
		fn, ok := goja.AssertFunction(call.Argument(1))
		if ok {
			m.mu.Lock()
			if m.handlers != nil {
				m.handlers[ev] = fn
			}
			m.mu.Unlock()
		}
		return goja.Undefined()
	})

	vm.Set("__lxSend", func(call goja.FunctionCall) goja.Value {
		ev := call.Argument(0).String()
		m.handleSend(ev, call.Argument(1))
		return goja.Undefined()
	})

	vm.Set("__lxRequest", m.jsRequest(vm))

	// ---- lx.utils 桥接 ----
	vm.Set("__goBytes", func(call goja.FunctionCall) goja.Value {
		s := call.Argument(0).String()
		enc := "utf8"
		if !isNothing(call.Argument(1)) {
			enc = call.Argument(1).String()
		}
		return m.newAB(vm, decodeString(s, enc))
	})
	vm.Set("__goBytesArr", func(call goja.FunctionCall) goja.Value {
		arr, ok := call.Argument(0).Export().([]any)
		if !ok {
			panic(vm.NewTypeError("期望数组"))
		}
		out := make([]byte, 0, len(arr))
		for _, v := range arr {
			f, _ := toFloat(v)
			out = append(out, byte(int64(f))&0xff)
		}
		return m.newAB(vm, out)
	})
	vm.Set("__lxBufferFrom", func(call goja.FunctionCall) goja.Value {
		v := call.Argument(0)
		if s, ok := v.Export().(string); ok {
			enc := "utf8"
			if !isNothing(call.Argument(1)) {
				enc = call.Argument(1).String()
			}
			return m.newAB(vm, decodeString(s, enc))
		}
		// 其他类型经 JS 侧 __bytesOf 处理
		r, err := vm.RunString("globalThis.__bytesOf")
		if err == nil {
			if fn, ok := goja.AssertFunction(r); ok {
				if res, err := fn(goja.Undefined(), v); err == nil {
					if ab, ok := res.Export().(goja.ArrayBuffer); ok {
						return m.newAB(vm, ab.Bytes())
					}
				}
			}
		}
		panic(vm.NewTypeError("buffer.from 参数类型不支持"))
	})
	vm.Set("__lxBufToString", func(call goja.FunctionCall) goja.Value {
		b := m.bytesViaJS(vm, call.Argument(0))
		enc := "utf8"
		if !isNothing(call.Argument(1)) {
			enc = call.Argument(1).String()
		}
		return vm.ToValue(encodeBytes(b, enc))
	})
	vm.Set("__lxMd5", func(call goja.FunctionCall) goja.Value {
		sum := md5.Sum([]byte(call.Argument(0).String()))
		return vm.ToValue(hex.EncodeToString(sum[:]))
	})
	vm.Set("__lxRandomBytes", func(call goja.FunctionCall) goja.Value {
		n, _ := toFloat(call.Argument(0).Export())
		if n < 0 || n > 1<<20 {
			panic(vm.NewTypeError("randomBytes 长度非法"))
		}
		b := make([]byte, int(n))
		if _, err := rand.Read(b); err != nil {
			panic(vm.NewTypeError("randomBytes: " + err.Error()))
		}
		return m.newAB(vm, b)
	})
	vm.Set("__lxAesEncrypt", func(call goja.FunctionCall) goja.Value {
		data := m.bytesViaJS(vm, call.Argument(0))
		mode := call.Argument(1).String()
		key := m.bytesViaJS(vm, call.Argument(2))
		iv := m.bytesViaJS(vm, call.Argument(3))
		out, err := aesEncrypt(data, mode, key, iv)
		if err != nil {
			panic(vm.NewTypeError(err.Error()))
		}
		return m.newAB(vm, out)
	})
	vm.Set("__lxRsaEncrypt", func(call goja.FunctionCall) goja.Value {
		data := m.bytesViaJS(vm, call.Argument(0))
		key := call.Argument(1).String()
		out, err := rsaEncrypt(data, key)
		if err != nil {
			panic(vm.NewTypeError(err.Error()))
		}
		return m.newAB(vm, out)
	})
	vm.Set("__lxInflate", func(call goja.FunctionCall) goja.Value {
		data := m.bytesViaJS(vm, call.Argument(0))
		out, err := zlibInflate(data)
		if err != nil {
			panic(vm.NewTypeError("zlib 解压失败: " + err.Error()))
		}
		return m.newAB(vm, out)
	})
	vm.Set("__lxDeflate", func(call goja.FunctionCall) goja.Value {
		data := m.bytesViaJS(vm, call.Argument(0))
		out, err := zlibDeflate(data)
		if err != nil {
			panic(vm.NewTypeError("zlib 压缩失败: " + err.Error()))
		}
		return m.newAB(vm, out)
	})

	// 轻量 navigator,兼容部分脚本的环境判断
	vm.RunString(`globalThis.navigator = globalThis.navigator || { userAgent: ` + quoteJS(hostUA) + `, platform: 'Win32' }`)

	if _, err := vm.RunString(bootstrap); err != nil {
		panic(err)
	}
}

// A timer created during a resolution must not start new HTTP work after that
// resolution is canceled. Timers created at initialization remain background work.
func (m *Manager) bindScopedTimers(vm *goja.Runtime) {
	for _, spec := range [][2]string{{"setTimeout", "clearTimeout"}, {"setInterval", "clearInterval"}, {"setImmediate", "clearImmediate"}} {
		set, ok := goja.AssertFunction(vm.Get(spec[0]))
		clear, clearOK := goja.AssertFunction(vm.Get(spec[1]))
		if !ok || !clearOK {
			continue
		}
		repeating := spec[0] == "setInterval"
		stops := make(map[goja.Value]func() bool)
		forget := func(id goja.Value) {
			if stop, ok := stops[id]; ok {
				delete(stops, id)
				stop()
			}
		}
		vm.Set(spec[1], func(call goja.FunctionCall) goja.Value {
			forget(call.Argument(0))
			result, err := clear(call.This, call.Arguments...)
			if err != nil {
				panic(err)
			}
			return result
		})
		vm.Set(spec[0], func(call goja.FunctionCall) goja.Value {
			fn, ok := goja.AssertFunction(call.Argument(0))
			if !ok {
				return goja.Undefined()
			}
			parent := m.requestCtx
			if parent.Err() != nil {
				return goja.Undefined()
			}
			var id goja.Value
			args := append([]goja.Value(nil), call.Arguments...)
			args[0] = vm.ToValue(func(inner goja.FunctionCall) goja.Value {
				if !repeating {
					forget(id)
				}
				if parent.Err() == nil {
					m.withRequestScope(parent, func() { callCB(vm, fn, inner.Arguments...) })
				}
				return goja.Undefined()
			})
			var err error
			id, err = set(call.This, args...)
			if err != nil {
				panic(err)
			}
			stops[id] = context.AfterFunc(parent, func() {
				m.runInLoop(func(vm *goja.Runtime) {
					forget(id)
					clear(goja.Undefined(), id)
				})
			})
			return id
		})
	}
}

// newAB 创建 JS ArrayBuffer 并包装为 goja.Value
func (m *Manager) newAB(vm *goja.Runtime, b []byte) goja.Value {
	return vm.ToValue(vm.NewArrayBuffer(b))
}

func scriptInfoObj(vm *goja.Runtime, info ScriptInfo) *goja.Object {
	o := vm.NewObject()
	o.Set("name", info.Name)
	o.Set("description", info.Description)
	o.Set("version", info.Version)
	o.Set("author", info.Author)
	o.Set("homepage", info.Homepage)
	return o
}

// bytesViaJS 把 JS 侧任意 buffer 形态(Uint8Array/ArrayBuffer/Array)转成 []byte
func (m *Manager) bytesViaJS(vm *goja.Runtime, v goja.Value) []byte {
	if isNothing(v) {
		return nil
	}
	if s, ok := v.Export().(string); ok {
		return []byte(s)
	}
	if ab, ok := v.Export().(goja.ArrayBuffer); ok {
		return ab.Bytes()
	}
	if fnV, err := vm.RunString("globalThis.__bytesOf"); err == nil {
		if fn, ok := goja.AssertFunction(fnV); ok {
			res, err := fn(goja.Undefined(), v)
			if err == nil {
				if ab, ok := res.Export().(goja.ArrayBuffer); ok {
					return ab.Bytes()
				}
			}
		}
	}
	panic(vm.NewTypeError("期望 buffer 类型"))
}

func isNothing(v goja.Value) bool {
	return v == nil || goja.IsUndefined(v) || goja.IsNull(v)
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case string:
		var f float64
		_, err := fmt.Sscanf(x, "%g", &f)
		return f, err == nil
	}
	return 0, false
}

func decodeString(s, enc string) []byte {
	switch enc {
	case "base64":
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil
		}
		return b
	case "hex":
		b, err := hex.DecodeString(s)
		if err != nil {
			return nil
		}
		return b
	case "gbk", "gb2312", "gb18030":
		// gbk 字节串应经 __bytesOf 走二进制路径;字符串输入按 UTF-8 处理
		return []byte(s)
	default: // utf8
		return []byte(s)
	}
}

func encodeBytes(b []byte, enc string) string {
	switch enc {
	case "base64":
		return base64.StdEncoding.EncodeToString(b)
	case "hex":
		return hex.EncodeToString(b)
	case "gbk", "gb2312", "gb18030":
		dec, err := simplifiedchinese.GBK.NewDecoder().Bytes(b)
		if err != nil {
			return ""
		}
		return string(dec)
	default:
		return string(b)
	}
}

func aesEncrypt(data []byte, mode string, key, iv []byte) ([]byte, error) {
	parts := strings.Split(strings.ToLower(mode), "-") // aes-128-cbc
	if len(parts) != 3 || parts[0] != "aes" {
		return nil, fmt.Errorf("不支持的加密模式: %s", mode)
	}
	var keyLen int
	switch parts[1] {
	case "128":
		keyLen = 16
	case "192":
		keyLen = 24
	case "256":
		keyLen = 32
	default:
		return nil, fmt.Errorf("不支持的密钥长度: %s", mode)
	}
	if len(key) != keyLen {
		return nil, fmt.Errorf("密钥长度应为 %d 字节,实际 %d", keyLen, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	data = pkcs7Pad(data, block.BlockSize())
	out := make([]byte, len(data))
	switch parts[2] {
	case "ecb":
		for off := 0; off < len(data); off += block.BlockSize() {
			block.Encrypt(out[off:off+block.BlockSize()], data[off:off+block.BlockSize()])
		}
	case "cbc":
		if len(iv) != block.BlockSize() {
			return nil, errors.New("CBC 模式 iv 长度非法")
		}
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, data)
	case "cfb":
		if len(iv) != block.BlockSize() {
			return nil, errors.New("CFB 模式 iv 长度非法")
		}
		cipher.NewCFBEncrypter(block, iv).XORKeyStream(out, data)
	case "ofb":
		if len(iv) != block.BlockSize() {
			return nil, errors.New("OFB 模式 iv 长度非法")
		}
		cipher.NewOFB(block, iv).XORKeyStream(out, data)
	case "ctr":
		if len(iv) != block.BlockSize() {
			return nil, errors.New("CTR 模式 iv 长度非法")
		}
		cipher.NewCTR(block, iv).XORKeyStream(out, data)
	default:
		return nil, fmt.Errorf("不支持的加密模式: %s", mode)
	}
	return out, nil
}

func pkcs7Pad(b []byte, blockSize int) []byte {
	n := blockSize - len(b)%blockSize
	return append(b, bytes.Repeat([]byte{byte(n)}, n)...)
}

func rsaEncrypt(data []byte, key string) ([]byte, error) {
	block, _ := pem.Decode([]byte(key))
	var pub *rsa.PublicKey
	if block != nil {
		switch block.Type {
		case "RSA PUBLIC KEY":
			p, err := x509.ParsePKCS1PublicKey(block.Bytes)
			if err != nil {
				return nil, err
			}
			pub = p
		default:
			p, err := x509.ParsePKIXPublicKey(block.Bytes)
			if err != nil {
				return nil, err
			}
			r, ok := p.(*rsa.PublicKey)
			if !ok {
				return nil, errors.New("非 RSA 公钥")
			}
			pub = r
		}
	} else {
		// 尝试裸 base64
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(key))
		if err != nil {
			return nil, errors.New("公钥格式无法识别")
		}
		if p, err := x509.ParsePKCS1PublicKey(raw); err == nil {
			pub = p
		} else if p, err := x509.ParsePKIXPublicKey(raw); err == nil {
			if r, ok := p.(*rsa.PublicKey); ok {
				pub = r
			}
		}
	}
	if pub == nil {
		return nil, errors.New("公钥解析失败")
	}
	max := pub.Size() - 11
	if max < 1 {
		// 模数过小的"公钥"会让 off += max 永不前进(死循环),
		// 且这段是 Go 代码,无法被 vm.Interrupt 打断,会拖死整个采集脚本线程。
		return nil, errors.New("公钥长度过小,无法用于加密")
	}
	var out []byte
	for off := 0; off < len(data); off += max {
		end := off + max
		if end > len(data) {
			end = len(data)
		}
		chunk, err := rsa.EncryptPKCS1v15(rand.Reader, pub, data[off:end])
		if err != nil {
			return nil, err
		}
		out = append(out, chunk...)
	}
	return out, nil
}

func zlibInflate(data []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, 20<<20))
}

func zlibDeflate(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	w.Close()
	return buf.Bytes(), nil
}

func quoteJS(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
