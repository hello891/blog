package router

import (
	"blog/conf"
	"blog/model"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"html/template"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/astaxie/beego/logs"
	jwt "github.com/dgrijalva/jwt-go"
	"github.com/labstack/echo"
	"github.com/labstack/echo/middleware"
	"github.com/zxysilent/utils"
)

var pool *sync.Pool
var log = logs.NewLogger()

func init() {
	os.Mkdir("logs/", 0777)
	log.SetLogger(logs.AdapterFile, `{"filename":"logs/app.log","maxdays":30}`)
	log.Async(1e3)
	pool = &sync.Pool{
		New: func() interface{} {
			return bytes.NewBuffer(make([]byte, 512))
		},
	}
}

// midLogrer 中间件-日志记录
func midLogger(next echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx echo.Context) (err error) {
		start := time.Now()
		if err = next(ctx); err != nil {
			ctx.Error(err)
		}
		stop := time.Now()
		buf := pool.Get().(*bytes.Buffer)
		buf.Reset()
		defer pool.Put(buf)
		buf.WriteString("[" + start.Format("2006-01-02 15:04:05") + "] ")
		buf.WriteString("\tip：" + ctx.RealIP())
		buf.WriteString("\tmethod：" + ctx.Request().Method)
		buf.WriteString("\tpath：" + ctx.Request().URL.Path)
		buf.WriteString("\turi：" + ctx.Request().RequestURI)
		buf.WriteString("\tspan：" + stop.Sub(start).String())
		buf.WriteString("\n")
		// 开发模式直接输出到控制台
		if conf.App.IsDev() {
			os.Stdout.Write(buf.Bytes())
			return
		}
		log.Info(buf.String())
		return
	}
}
func midRecover(next echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx echo.Context) error {
		defer func() {
			if r := recover(); r != nil {
				err, ok := r.(error)
				if !ok {
					err = fmt.Errorf("%v", r)
				}
				stack := make([]byte, 1<<10)
				length := runtime.Stack(stack, false)
				// stdlog.Println(string(stack[:length]))
				os.Stdout.Write(stack[:length])
				ctx.Error(err)
			}
		}()
		return next(ctx)
	}
}

// HTTPErrorHandler 全局错误捕捉
func HTTPErrorHandler(err error, ctx echo.Context) {
	if !ctx.Response().Committed {
		if strings.Contains(err.Error(), "404") {
			ctx.NoContent(404)
			//ctx.HTML(404, "Not Found")
			// ctx.Redirect(302, "/404.html")
		} else {
			ctx.JSON(utils.NewErrSvr(err.Error()))
		}
	}
}

// 跨越配置
var crosConfig = middleware.CORSConfig{
	AllowOrigins: []string{"*"},
	AllowHeaders: []string{echo.HeaderOrigin, echo.HeaderContentType, echo.HeaderAccept, echo.HeaderAuthorization},
}

// TplRender is a custom html/template renderer for Echo framework
type TplRender struct {
	templates *template.Template
}

// Render renders a template document
func (t *TplRender) Render(w io.Writer, name string, data interface{}, ctx echo.Context) error {
	// 获取数据配置项
	if mp, is := data.(map[string]interface{}); is {
		mp["title"] = model.MapOpts.MustGet("title")
		mp["favicon"] = model.MapOpts.MustGet("favicon")
		mp["analytic"] = model.MapOpts.MustGet("analytic")
		mp["site_url"] = model.MapOpts.MustGet("site_url")
		mp["logo_url"] = model.MapOpts.MustGet("logo_url")
		mp["keywords"] = model.MapOpts.MustGet("keywords")
		mp["miitbeian"] = model.MapOpts.MustGet("miitbeian")
		mp["weibo_url"] = model.MapOpts.MustGet("weibo_url")
		mp["custom_js"] = model.MapOpts.MustGet("custom_js")
		mp["github_url"] = model.MapOpts.MustGet("github_url")
		mp["description"] = model.MapOpts.MustGet("description")
	}
	//开发模式
	//每次强制读取模板
	//每次强制加载函数
	if conf.App.IsDev() {
		funcMap := template.FuncMap{"str2html": Str2html, "date": Date, "md5": Md5}
		t.templates = utils.LoadTmpl("./view", funcMap)
	}
	return t.templates.ExecuteTemplate(w, name, data)
}

// Str2html Convert string to template.HTML type.
func Str2html(raw string) template.HTML {
	return template.HTML(raw)
}

// Date Date
func Date(t time.Time, format string) string {
	return t.Format(format) //"2006-01-02 15:04:05"
}

// Md5 Md5
func Md5(str string) string {
	ctx := md5.New()
	ctx.Write([]byte(str))
	return hex.EncodeToString(ctx.Sum(nil))
}

// 初始化模板和函数
func initRender() *TplRender {
	funcMap := template.FuncMap{"str2html": Str2html, "date": Date, "md5": Md5}
	tpl := utils.LoadTmpl("./view", funcMap)
	return &TplRender{
		templates: tpl,
	}
}

// midJwt 中间件-jwt验证
func midJwt(next echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx echo.Context) error {
		// query form 查找 token
		tokenString := ctx.FormValue("token")
		if tokenString == "" {
			// header 查找token
			tokenString = ctx.Request().Header.Get(echo.HeaderAuthorization)
			if tokenString == "" {
				ctx.JSON(utils.NewErrJwt(`请重新登陆`, `未发现jwt`))
				return nil
			}
			// Bearer token
			tokenString = tokenString[7:] //len("Bearer ")
		}
		jwtAuth := &model.JwtClaims{}
		jwt, err := jwt.ParseWithClaims(tokenString, jwtAuth, func(token *jwt.Token) (interface{}, error) {
			return []byte("zxy.sil.ent"), nil
		})
		if err == nil && jwt.Valid {
			ctx.Set("auth", jwtAuth)
			ctx.Set("uid", jwtAuth.Id)
		} else {
			return ctx.JSON(utils.NewErrJwt(`请重新登陆","jwt验证失败`))
		}
		// 自定义头
		ctx.Response().Header().Set(echo.HeaderServer, "dev")
		return next(ctx)
	}
}

// // midAdmin 中间件-后台管理权限验证
// func midAdmin(next echo.HandlerFunc) echo.HandlerFunc {
// 	return func(ctx echo.Context) error {
// 		ctx.Response().Header().Set(echo.HeaderServer, "admin")
// 		authObj := ctx.Get("auth")
// 		auth := authObj.(*model.JwtClaims)
// 		//RTea uint32 = 27 //teacher 		教师-课程老师
// 		if auth.Role.Gte(27) { //大于等于某个权限
// 			return next(ctx)
// 		}
// 		return ctx.JSON(utils.NewErrDeny(`对不起，你无法进行操作^_^!`, "当前用户无后台管理权限"))
// 	}
// }
