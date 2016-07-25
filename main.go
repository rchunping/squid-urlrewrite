package main

import (
	"bufio"
	"fmt"
	"os/signal"
	"syscall"
	//"io/ioutil"
	"log"
	"log/syslog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type actionType int

const (
	REWRITE actionType = iota
	REDIRECT
	REDIRECT_301
)

type RewritePattern struct {
	Pattern *regexp.Regexp
	Target  string
	Type    actionType
}

var (
	PROG_PATH          string
	rewriter_exit_chan chan int    = make(chan int, 1)
	response_chan      chan string = make(chan string, 1024*10)
	config_paths       []string
	signal_hup_chan    chan os.Signal = make(chan os.Signal, 1)
	stdin_line_chan    chan string    = make(chan string, 100)
)

func parseRewritePatterns() (rewritePattern []RewritePattern, isDebug bool) {

	rewritePattern = make([]RewritePattern, 0)
	isDebug = false

	for _, f := range config_paths {
		fp, err := os.Open(f)
		if err != nil {
			continue
		} else {
			log.Printf("use configure file: %s", f)
		}
		defer fp.Close()

		scanner := bufio.NewScanner(fp)
		lineno := 0
		for scanner.Scan() {
			line := scanner.Text()
			line = strings.TrimSpace(line)
			lineno++

			if line == "" {
				continue
			}
			if line[0] == '#' {
				continue
			}

			fs := strings.Fields(line)

			if len(fs) == 2 && fs[0] == "loglevel" {
				if fs[1] == "debug" {
					isDebug = true
				}
				continue
			}

			if len(fs) != 3 || !(fs[0] == "rewrite" || fs[0] == "redirect") {
				log.Printf("configure parse error: %s:%d : %s", f, lineno, "format error")
				os.Exit(1)
			}

			rp := RewritePattern{}

			if reg, err := regexp.Compile(fs[1]); err == nil {
				rp.Pattern = reg
				rp.Target = fs[2]

				if fs[0] == "rewrite" {
					rp.Type = REWRITE
				} else {
					rp.Type = REDIRECT

					if len(fs[2]) >= 4 {
						n := fs[2][0:4]
						if n == "301;" || n == "302;" {
							rp.Target = fs[2][4:]
						}
						if n == "301;" {
							rp.Type = REDIRECT_301
						}
					}
				}

				rewritePattern = append(rewritePattern, rp)

			} else {
				log.Printf("configure regexp parse error: %s:%d : %s", f, lineno, err.Error())
				os.Exit(1)
			}

		}

		if err := scanner.Err(); err != nil {
			log.Printf("reading configure file error: %s <%s>", err.Error(), f)
			os.Exit(1)
		}

	}

	return
}

func isInt(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

func AddResponse(s string) {
	response_chan <- s
}
func WriterResponseLines() {
	out := bufio.NewWriter(os.Stdout)
	for {
		line := <-response_chan
		out.WriteString(line)
		out.WriteString("\n")
		out.Flush()
	}
}

func doRewriter(id, url string, rwpatterns *[]RewritePattern, isDebug bool) {

	sep := " "
	if id == "" {
		sep = ""
	}

	rurl := ""

	var matched RewritePattern

	for _, rwp := range *rwpatterns {
		ms := rwp.Pattern.FindStringSubmatch(url)

		if len(ms) > 0 {
			rurl = rwp.Target

			for i, s := range ms {
				rurl = strings.Replace(rurl, fmt.Sprintf("$%d", i), s, -1)
			}

			matched = rwp
			break
		}

	}

	if rurl == "" {
		AddResponse(fmt.Sprintf("%s%sERR", id, sep))
		if isDebug {
			log.Printf("[nochange] %s -> -", url)
		}
	} else {
		// fix '"' in rurl
		rurl = strings.Replace(rurl, "\"", "%22", -1)

		if matched.Type == REWRITE {
			AddResponse(fmt.Sprintf("%s%sOK rewrite-url=\"%s\"", id, sep, rurl))
			if isDebug {
				log.Printf("[rewrite] %s -> %s", url, rurl)
			}
		} else {
			s30X := "302"
			if matched.Type == REDIRECT_301 {
				s30X = "301"
			}

			AddResponse(fmt.Sprintf("%s%sOK status=%s url=\"%s\"", id, sep, s30X, rurl))
			if isDebug {
				log.Printf("[redirect] %s -> %s", url, rurl)
			}
		}
	}

}

func StartRewriter() {

	rewritePatterns, isDebug := parseRewritePatterns()

	log.Printf("got %d rewrite rules.", len(rewritePatterns))

	var line string

scanloop:
	for {

		select {
		case line = <-stdin_line_chan:
		case <-signal_hup_chan:
			log.Printf("got SIGHUP to reload configure.")
			break scanloop
		}

		line = strings.TrimSpace(line)

		id := ""
		url := ""

		fs := strings.Fields(line)

		concurrency := false
		if len(fs) >= 2 && isInt(fs[0]) {
			concurrency = true
			id = fs[0]
			url = fs[1]
		} else if len(fs) >= 1 {
			url = fs[0]
		}

		if concurrency {
			go doRewriter(id, url, &rewritePatterns, isDebug)
		} else {
			doRewriter(id, url, &rewritePatterns, isDebug)
		}
	}

	// exit rewriter
	log.Printf("stop rewriter")
	rewriter_exit_chan <- 1
}

func main() {
	PROG_PATH, _ = filepath.Abs(filepath.Dir(os.Args[0]))

	//log.SetOutput(os.Stderr) // it's default
	if _slog, err := syslog.New(syslog.LOG_DEBUG, "squid-urlrewrite"); err == nil {
		log.SetOutput(_slog)
	}

	config_paths = []string{
		strings.TrimRight(PROG_PATH, "/") + "/squid-urlrewrite.conf",
		"/usr/local/etc/squid-urlrewrite.conf",
		"/etc/squid-urlrewrite.conf",
	}

	// catch SIGHUP to reload configure
	signal.Notify(signal_hup_chan, syscall.SIGHUP)

	go WriterResponseLines()

	// scan stdin line by line
	inscanner := bufio.NewScanner(os.Stdin)
	go func() {
		for inscanner.Scan() {
			stdin_line_chan <- inscanner.Text()
		}
		err := inscanner.Err()
		if err != nil {
			log.Printf("reading stdin error: %s", err.Error())
			os.Exit(1)
		}
		log.Printf("exit.")
		os.Exit(0)
	}()

	rewriter_exit_chan <- 1
	for {
		// restart rewriter
		<-rewriter_exit_chan
		log.Printf("start rewriter")
		go StartRewriter()
	}
}
