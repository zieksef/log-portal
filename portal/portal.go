package portal

import (
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	timeLayout = "2006-01-02T15-04-05.000"
)

var (
	location, _         = time.LoadLocation("Asia/Shanghai")
	cleanInterval int64 = 3 // day
)

type Portal struct {
	// Remote file url.
	URL string

	// Stands for the `last n bytes` for the initial fetch.
	Tail int64

	// Fetch log interval.
	Interval int64

	// Local directory for files writing in.
	Dir string

	// Archived local logs lifetime in days.
	Lifetime int64

	// If output remote log into console.
	ConsolePortal bool

	// If write remote log into file.
	FilePortal bool

	// Last remote log file size.
	offset int64

	// Remote log filename.
	filename string

	// Current opened file.
	file *os.File

	writer io.Writer

	client *http.Client

	ticker *time.Ticker
}

func New(url string, interval int64, tail int64) *Portal {
	return &Portal{
		URL:           url,
		Tail:          tail,
		Interval:      interval,
		client:        http.DefaultClient,
		ConsolePortal: true,
	}
}

func (p *Portal) Init() error {
	filename := p.LogName()

	if filename == "" {
		return fmt.Errorf("invalid log file url: {%s}", p.URL)
	}

	p.filename = filename

	return nil
}

// SetupWriter must be called after Init().
func (p *Portal) SetupWriter(disableConsole bool, enableFile bool, dir string, lifetime int64) error {
	var writers []io.Writer

	if disableConsole {
		p.ConsolePortal = false
	}

	if p.ConsolePortal {
		writers = append(writers, os.Stdout)
	}

	if enableFile {
		if dir == "" {
			return fmt.Errorf("invalid log dir[%s]", dir)
		}

		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("make mkdir[%s]: %v", dir, err)
		}

		path := filepath.Join(dir, p.filename)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0644)

		if err != nil {
			return fmt.Errorf("open file[%s]: %v", path, err)
		}

		p.file = file
		p.Dir = dir
		p.Lifetime = lifetime
		p.FilePortal = true

		writers = append(writers, p.file)
	}

	multiWriter := io.MultiWriter(writers...)
	p.writer = multiWriter

	return nil
}

func (p *Portal) LogName() string {
	fields := strings.Split(p.URL, "/")
	if len(fields) > 0 {
		return fields[len(fields)-1]
	}
	return ""
}

func (p *Portal) LogSize() (int64, error) {
	req, reqErr := http.NewRequest(http.MethodHead, p.URL, nil)
	if reqErr != nil {
		return 0, fmt.Errorf("create new request: %v", reqErr)
	}

	resp, doErr := p.client.Do(req)
	if doErr != nil {
		return 0, fmt.Errorf("http head: %v", doErr)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return resp.ContentLength, nil
}

func (p *Portal) initialFetch() (int64, error) {
	currSize, getErr := p.LogSize()
	if getErr != nil {
		return 0, fmt.Errorf("get log size: %v\n", getErr)
	}

	if currSize == 0 {
		return 0, nil
	}

	if currSize < p.Tail {
		return 0, nil
	}

	start := currSize - p.Tail

	if err := p.FetchIncrContent(start, currSize); err != nil {
		return 0, err
	}

	return currSize, nil
}

func (p *Portal) FetchIncrContent(start int64, end int64) error {
	req, reqErr := http.NewRequest(http.MethodGet, p.URL, nil)
	if reqErr != nil {
		return fmt.Errorf("create new request: %v", reqErr)
	}

	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end-1))

	resp, doErr := p.client.Do(req)
	if doErr != nil {
		return fmt.Errorf("http get: %v", doErr)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	if _, err := io.Copy(p.writer, resp.Body); err != nil {
		return fmt.Errorf("copy response body: %v", err)
	}

	return nil
}

func (p *Portal) RotateFile() error {
	if !p.FilePortal {
		return nil
	}

	if err := p.file.Sync(); err != nil {
		return fmt.Errorf("sync file: %v", err)
	}

	if err := p.file.Close(); err != nil {
		return fmt.Errorf("close file: %v", err)
	}

	path := filepath.Join(p.Dir, p.filename) // old path

	timeStr := time.Now().In(location).Format(timeLayout)

	prefix := p.filename
	fields := strings.Split(p.filename, ".") // [access] . [log]
	if len(fields) == 2 {
		prefix = fields[0]
	}

	archivedFilename := fmt.Sprintf("%s-%s.log", prefix, timeStr)
	archivedPath := filepath.Join(p.Dir, archivedFilename) // new path

	if err := os.Rename(path, archivedPath); err != nil {
		return fmt.Errorf("rename file: %v", err)
	}

	newFile, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("open new file: %v", err)
	}

	p.file = newFile

	writers := []io.Writer{p.file}
	if p.ConsolePortal {
		writers = append(writers, os.Stdout)
	}
	p.writer = io.MultiWriter(writers...)

	return nil
}

func (p *Portal) Clean() error {
	match := func(filename string) bool {
		now := time.Now().In(location)

		fields1 := strings.SplitN(filename, "-", 2)
		if len(fields1) != 2 {
			return false
		}

		fn := fields1[1]

		suffix := ".log"

		if !strings.HasSuffix(fn, suffix) {
			return false
		}

		prefix := fn[:len(fn)-len(suffix)]

		t, err := time.ParseInLocation(timeLayout, prefix, location)
		if err != nil {
			return false
		}

		if now.UnixMilli()-t.UnixMilli() < p.Lifetime*24*60*60*1000 {
			return false
		}

		return true
	}

	// 使用 filepath.WalkDir 遍历目录
	err := filepath.WalkDir(p.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		filename := d.Name()
		if filename == p.filename {
			return nil
		}

		if !match(filename) {
			return nil
		}

		if osErr := os.Remove(filename); osErr != nil {
			return osErr
		}

		return nil
	})

	if err != nil {
		return err
	}

	return nil
}

func (p *Portal) Start() {
	p.ticker = time.NewTicker(time.Duration(p.Interval) * time.Second)
	defer p.ticker.Stop()

	fmt.Printf("[Portal]: fetching {%s}...\n", p.URL)

	var currSize int64
	var getErr error

	if p.FilePortal {
		if cleanInterval > p.Lifetime {
			cleanInterval = p.Lifetime
		}

		go func() {
			ticker := time.NewTicker(time.Second * 10)
			defer ticker.Stop()

			for range ticker.C {
				_ = p.Clean()
			}
		}()
	}

	currSize, getErr = p.initialFetch()
	if getErr != nil {
		fmt.Printf("initial fetch: %v\n", getErr)
	}

	for range p.ticker.C {
		currSize, getErr = p.LogSize()
		if getErr != nil {
			fmt.Printf("get log size: %v\n", getErr)
			continue
		}

		if currSize > p.offset {
			if err := p.FetchIncrContent(p.offset, currSize); err != nil {
				fmt.Printf("fetch incremental content: %v\n", err)
				continue
			}

			p.offset = currSize
			continue
		}

		if currSize < p.offset {
			// remote log rotated
			if err := p.RotateFile(); err != nil {
				fmt.Printf("rotate file: %v\n", err)
			}

			p.offset = 0
		}
	}

}

func (p *Portal) Finalize() {
	// rotate once
	_ = p.RotateFile()

	_ = p.file.Sync()
	_ = p.file.Close()

	p.ticker.Stop()
}
