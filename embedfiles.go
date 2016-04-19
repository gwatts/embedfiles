// (c) Gareth Watts 2016
// Licensed under an MIT license
// See the LICENSE file for details
// github.com/gwatts/embedfiles

/*
Command embedfiles converts one or more files into Go source code so that
they may be compiled directly into a Go program.

It is intended to be run via the go generate tool and creates an instance
variable that provides file-like access to the embedded assets using
a bytes.Reader.

Additionally each file instance complies with the http.File interface.

By default the generated package does not provide a type compatible with the
http.FileServer interface to avoid importing net/http - Supplying the
-include-http flag will enable support for that interface.

For example to embed the html and css files in an assets directory into
a new file called assets.go as part of a package called webserver:

  embedfiles -filename assets.go -package webserver -include-http -var Assets assets/*.html assets/*.css

Code within the assets package could then open index.html for read:

  f, err := Assets.Open("assets/index.html")

or could use an instance as a file server (assuming the -include-http flag was set):

  log.Fatal(http.ListenAndServe(":8080", http.FileServer(Assets))

As each call to embedfiles generates a completely self-contained .go file,
multiple independent .go files can be generated and compiled into a single
package by using different -varname options, allowing for discrete groups
of files to be assigned to different variable names.

  Usage:  embedfiles [arguments] <file glob> [<file glob> ...]

  Arguments:

    -filename string
          File to write go output to.  Defaults to stdout (default "-")
    -include-http
          If true then the generated file will import net/http and comply with the http.FileSystem interface
    -package string
          Package name to use for output file. (default "main")
    -var string
          Variable name to assign the assets to.  Start with a capital letter to export from the package (default "assets")

*/
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"
)

var (
	fn      = flag.String("filename", "-", "File to write go output to.  Defaults to stdout")
	pkg     = flag.String("package", "main", "Package name to use for output file.")
	prefix  = flag.String("var", "assets", "Variable name to assign the assets to.  Start with a capital letter to export from the package")
	incHTTP = flag.Bool("include-http", false, "If true then the generated file will import net/http and comply with the http.FileSystem interface")
)

var header = `package {{ .Pkg }}

// Generated by github.com/gwatts/embedfiles
// at {{ .Time }}

import (
	"bytes"
	"errors"
	{{ if .IncludeHTTP -}}
	"net/http"
	{{- end }}
	"os"
	"path"
	"strings"
	"time"
)

type {{ .Prefix }}File struct {
	*bytes.Reader
	fi {{ .Prefix }}FI
}

func (f *{{ .Prefix}}File) Close() error { return nil }
func (f *{{ .Prefix}}File) Readdir(count int) ([]os.FileInfo, error) { return nil, errors.New("Denied")}
func (f *{{ .Prefix }}File) Stat() (os.FileInfo, error) { return f.fi, nil }

type {{ .Prefix }}FI struct {
	name  string
	size  int64
	mode  os.FileMode
	ftime time.Time
}

func (fi {{ .Prefix }}FI) Name() string       { return fi.name }
func (fi {{ .Prefix }}FI) Close() error       { return nil }
func (fi {{ .Prefix }}FI) Size() int64        { return fi.size }
func (fi {{ .Prefix }}FI) Mode() os.FileMode  { return fi.mode }
func (fi {{ .Prefix }}FI) ModTime() time.Time { return fi.ftime }
func (fi {{ .Prefix }}FI) IsDir() bool        { return false }
func (fi {{ .Prefix }}FI) Sys() interface{}   { return nil }

type {{ .Prefix }}T struct {
	filenames []string
	files     map[string]struct {
		ts int64
		offset int
		size   int
	}
	data [{{ .DataSize }}]byte
}

func (fs *{{ .Prefix }}T) Filenames() []string { return fs.filenames }

// Open returns a bytes.Reader for the given filename.
{{ if .IncludeHTTP -}}
func (fs *{{ .Prefix }}T) Open(filename string) (http.File, error) {
{{- else -}}
func (fs *{{ .Prefix }}T) Open(filename string) (*{{ .Prefix }}File, error) {
{{- end }}
	filename = strings.TrimPrefix(filename, "/")
	entry, ok := fs.files[filename]
	if !ok {
		return nil, os.ErrNotExist
	}
	b := fs.data[entry.offset : entry.offset+entry.size]
	return &{{ .Prefix }}File{
		Reader: bytes.NewReader(b),
		fi:     {{ .Prefix }}FI{name: path.Base(filename), size: int64(entry.size), mode: os.ModePerm, ftime: time.Unix(entry.ts, 0)},
	}, nil
}

`

var headerTmpl = template.Must(template.New("header").Parse(header))

func init() {
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "embedfiles reads one or more files and embeds them into a .go source file")
		fmt.Fprintln(os.Stderr, "for compilation into a program.")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Usage:  embedfiles [arguments] <file glob> [<file glob> ...]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Arguments:")
		fmt.Fprintln(os.Stderr)
		flag.PrintDefaults()
	}
	flag.Parse()
}

func fail(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(100)
}

func fmtBytes(out io.Writer, in io.Reader) (bytesRead, bytesWritten int, err error) {
	const digits = "0123456789abcdef"

	buf := make([]byte, 16)
	fbuf := make([]byte, 0, 64)

	for {
		rn, rerr := in.Read(buf)
		if rn > 0 {
			fbuf = append(fbuf, "   "...)
			for i := 0; i < rn; i++ {
				fbuf = append(fbuf, ' ', '0', 'x', digits[buf[i]>>4], digits[buf[i]&0xf], ',')
			}
			fbuf = append(fbuf, '\n')
			wn, werr := out.Write(fbuf)
			bytesWritten += wn
			if werr != nil {
				return bytesRead, bytesWritten, werr
			}
			bytesRead += rn
			fbuf = fbuf[0:0]
		}
		if rerr == io.EOF {
			return bytesRead, bytesWritten, nil
		} else if rerr != nil {
			return bytesRead, bytesWritten, rerr
		}
	}
}

type entry struct {
	ts     int64
	offset int
	size   int
}

func generate(w io.Writer, pkg, prefix string, globs []string) error {
	var offset, fcount int
	var filenames []string
	filemap := make(map[string]entry)
	databuf := new(bytes.Buffer)

	for _, pattern := range globs {
		names, err := filepath.Glob(pattern)
		if err != nil {
			return err
		}

		for _, name := range names {
			var ts int64
			fcount++
			fmt.Fprintln(databuf, "\n    //", name)
			f, err := os.Open(name)
			if err != nil {
				return fmt.Errorf("Failed to read %s: %v", name, err)
			}
			name = strings.TrimPrefix(name, "/")
			filenames = append(filenames, name)
			if fi, err := f.Stat(); err == nil {
				ts = fi.ModTime().Unix()
			}
			br, _, err := fmtBytes(databuf, f)
			f.Close()
			if err != nil {
				return fmt.Errorf("Failed to process %s: %v", name, err)
			}
			filemap[name] = entry{ts: ts, offset: offset, size: br}
			offset += br
		}
	}
	if fcount == 0 {
		return errors.New("No files found")
	}

	sort.Strings(filenames)
	headerTmpl.Execute(w, map[string]interface{}{
		"DataSize":    offset,
		"Filenames":   fmt.Sprintf("%#v", filenames),
		"IncludeHTTP": *incHTTP,
		"Pkg":         pkg,
		"Prefix":      prefix,
		"Time":        time.Now().Format(time.UnixDate),
	})

	fmt.Fprintf(w, "var %s = &%sT{\n", prefix, prefix)
	fmt.Fprintf(w, "    filenames: %#v,\n", filenames)
	fmt.Fprintf(w, "    files: map[string]struct{ts int64; offset int; size int}{\n")
	for fn, entry := range filemap {
		fmt.Fprintf(w, "        %#v: {%d, %d, %d},\n", fn, entry.ts, entry.offset, entry.size)
	}

	fmt.Fprintln(w, "    },")
	fmt.Fprintf(w, "    data: [%d]byte{\n", offset)
	databuf.WriteTo(w)
	fmt.Fprintln(w, "    },")
	fmt.Fprintln(w, "}")

	if w, ok := w.(io.Closer); ok {
		w.Close()
	}
	return nil
}

func main() {
	var out io.Writer = os.Stdout

	if flag.NArg() == 0 {
		fail("No globs specified")
	}

	if *fn != "" && *fn != "-" {
		f, err := os.Create(*fn)
		if err != nil {
			fail("Failed to open file for write: %v", err)
		}
		out = f
	}

	if err := generate(out, *pkg, *prefix, flag.Args()); err != nil {
		fail(err.Error())
	}
}
