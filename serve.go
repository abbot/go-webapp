package webapp

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const errorPageShort = "<html><body><h1>500 Internal server error</h1></body></html>"
const errorPageDetailed = "<html><body><h1>500 Internal server error</h1><p>A runtime error has just happened:</p><ul><li>%v</li></ul><p>Stack trace of the problem:</p><pre>%s</pre></body></html>"

const ApacheTime = "02/Jan/2006:15:04:05 -0700"

type LogRecord struct {
	http.ResponseWriter

	Host             string
	Indent           string
	RequestStarted   time.Time
	RequestCompleted time.Time
	Request          string
	Status           int
	Bytes            uint64
	Referer          string
	UserAgent        string
}

func (rec *LogRecord) Write(p []byte) (n int, err error) {
	n, err = rec.ResponseWriter.Write(p)
	rec.Bytes += uint64(n)
	return n, err
}

func (rec *LogRecord) WriteHeader(status int) {
	rec.Status = status
	rec.ResponseWriter.WriteHeader(status)
}

type Formatter func(*LogRecord) string

func CombinedFormat(rec *LogRecord) string {
	return fmt.Sprintf(`%s - %s [%s] "%s" %d %d "%s" "%s"`,
		rec.Host, rec.Indent, rec.RequestStarted.Format(ApacheTime),
		rec.Request, rec.Status, rec.Bytes, rec.Referer, rec.UserAgent)
}

func PerfFormat(rec *LogRecord) string {
	return fmt.Sprintf(`%s - %s [%s] "%s" %d %d %dms`,
		rec.Host, rec.Indent, rec.RequestStarted.Format(ApacheTime),
		rec.Request, rec.Status, rec.Bytes,
		rec.RequestCompleted.Sub(rec.RequestStarted).Nanoseconds()/1e6)
}

type App struct {
	StackIn500 bool
	StackInLog bool
	Handler    http.HandlerFunc

	Errors  chan *string
	Loggers []chan *LogRecord
}

func NewApp(h http.HandlerFunc, detailed_stacks bool) *App {
	app := &App{
		StackIn500: detailed_stacks,
		StackInLog: detailed_stacks,
		Handler:    h,
		Loggers:    make([]chan *LogRecord, 0),
	}
	return app
}

func (app *App) HandlePanic(w http.ResponseWriter, r *http.Request) {
	if e := recover(); e != nil {
		w.WriteHeader(500)
		if app.StackIn500 {
			fmt.Fprintf(w, errorPageDetailed, e, Stack(2))
		} else {
			fmt.Fprint(w, errorPageShort)
		}
		if app.Errors != nil {
			if app.StackInLog {
				msg := fmt.Sprintf("[%s] [panic] %v [at %s]\n%s", time.Now().Format(ApacheTime), e, where(2), Stack(2))
				app.Errors <- &msg
			} else {
				msg := fmt.Sprintf("[%s] [panic] %v [at %s]", time.Now().Format(ApacheTime), e, where(2))
				app.Errors <- &msg
			}
		}
	}
}

func (app App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec := &LogRecord{
		ResponseWriter: w,
		Indent:         "-",
		RequestStarted: time.Now(),
		// kind of cheating
		Request:   r.Method + " " + r.RequestURI + " " + r.Proto,
		Status:    http.StatusOK,
		Bytes:     0,
		Referer:   r.Referer(),
		UserAgent: r.UserAgent(),
	}

	if n := strings.LastIndex(r.RemoteAddr, ":"); n != -1 {
		rec.Host = r.RemoteAddr[:n]
	} else {
		rec.Host = r.RemoteAddr
	}
	defer app.HandlePanic(rec, r)
	app.Handler(rec, r)
	rec.RequestCompleted = time.Now()

	for _, logger := range app.Loggers {
		logger <- rec
	}
}

func (app *App) AddLogger(f Formatter, log *log.Logger) {
	ch := make(chan *LogRecord, 1000)
	app.Loggers = append(app.Loggers, ch)
	go func() {
		for {
			rec := <-ch
			log.Print(f(rec))
		}
	}()
}

func (app *App) ErrorLogger(log *log.Logger) {
	ch := make(chan *string, 1000)
	app.Errors = ch
	go func() {
		for {
			msg := <-ch
			log.Print(msg)
		}
	}()
}

func FileLogger(filename string) (*log.Logger, error) {
	f, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
	if err != nil {
		return nil, err
	}
	return log.New(f, "", 0), nil
}

func HandlerFunc(h http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r)
	}
}
