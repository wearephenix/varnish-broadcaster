package ini

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
)

const (
	// Name for default section. You can use this constant or the string literal.
	// In most of cases, an empty string is all you need to access the section.
	DEFAULT_SECTION = "DEFAULT"

	// Maximum allowed depth when recursively substituing variable names.
	_DEPTH_VALUES = 99
	_VERSION      = "1.21.1"
)

// Version returns current package version literal.
func Version() string {
	return _VERSION
}

var (
	// Delimiter to determine or compose a new line.
	// This variable will be changed to "\r\n" automatically on Windows
	// at package init time.
	LineBreak = "\n"

	// Variable regexp pattern: %(variable)s
	varPattern = regexp.MustCompile(`%\(([^\)]+)\)s`)

	// Indicate whether to align "=" sign with spaces to produce pretty output
	// or reduce all possible spaces for compact format.
	PrettyFormat = true

	// Explicitly write DEFAULT section header
	DefaultHeader = false
)

func init() {
	if runtime.GOOS == "windows" {
		LineBreak = "\r\n"
	}
}

func inSlice(str string, s []string) bool {
	for _, v := range s {
		if str == v {
			return true
		}
	}
	return false
}

// dataSource is an interface that returns object which can be read and closed.
type dataSource interface {
	ReadCloser() (io.ReadCloser, error)
}

// sourceFile represents an object that contains content on the local file system.
type sourceFile struct {
	name string
}

func (s sourceFile) ReadCloser() (_ io.ReadCloser, err error) {
	return os.Open(s.name)
}

type bytesReadCloser struct {
	reader io.Reader
}

func (rc *bytesReadCloser) Read(p []byte) (n int, err error) {
	return rc.reader.Read(p)
}

func (rc *bytesReadCloser) Close() error {
	return nil
}

// sourceData represents an object that contains content in memory.
type sourceData struct {
	data []byte
}

func (s *sourceData) ReadCloser() (io.ReadCloser, error) {
	return &bytesReadCloser{bytes.NewReader(s.data)}, nil
}

// File represents a combination of a or more INI file(s) in memory.
type File struct {
	// Should make things safe, but sometimes doesn't matter.
	BlockMode bool
	// Make sure data is safe in multiple goroutines.
	lock sync.RWMutex

	// Allow combination of multiple data sources.
	dataSources []dataSource
	// Actual data is stored here.
	sections map[string]*Section

	// To keep data in order.
	sectionList []string

	options LoadOptions

	NameMapper
	ValueMapper
}

// newFile initializes File object with given data sources.
func newFile(dataSources []dataSource, opts LoadOptions) *File {
	return &File{
		BlockMode:   true,
		dataSources: dataSources,
		sections:    make(map[string]*Section),
		sectionList: make([]string, 0, 10),
		options:     opts,
	}
}

func parseDataSource(source interface{}) (dataSource, error) {
	switch s := source.(type) {
	case string:
		return sourceFile{s}, nil
	case []byte:
		return &sourceData{s}, nil
	default:
		return nil, fmt.Errorf("error parsing data source: unknown type '%s'", s)
	}
}

type LoadOptions struct {
	// Loose indicates whether the parser should ignore nonexistent files or return error.
	Loose bool
	// Insensitive indicates whether the parser forces all section and key names to lowercase.
	Insensitive bool
	// IgnoreContinuation indicates whether to ignore continuation lines while parsing.
	IgnoreContinuation bool
	// AllowBooleanKeys indicates whether to allow boolean type keys or treat as value is missing.
	// This type of keys are mostly used in my.cnf.
	AllowBooleanKeys bool
}

func LoadSources(opts LoadOptions, source interface{}, others ...interface{}) (_ *File, err error) {
	sources := make([]dataSource, len(others)+1)
	sources[0], err = parseDataSource(source)
	if err != nil {
		return nil, err
	}
	for i := range others {
		sources[i+1], err = parseDataSource(others[i])
		if err != nil {
			return nil, err
		}
	}
	f := newFile(sources, opts)
	if err = f.Reload(); err != nil {
		return nil, err
	}
	return f, nil
}

// Load loads and parses from INI data sources.
// Arguments can be mixed of file name with string type, or raw data in []byte.
// It will return error if list contains nonexistent files.
func Load(source interface{}, others ...interface{}) (*File, error) {
	return LoadSources(LoadOptions{}, source, others...)
}

// LooseLoad has exactly same functionality as Load function
// except it ignores nonexistent files instead of returning error.
func LooseLoad(source interface{}, others ...interface{}) (*File, error) {
	return LoadSources(LoadOptions{Loose: true}, source, others...)
}

// InsensitiveLoad has exactly same functionality as Load function
// except it forces all section and key names to be lowercased.
func InsensitiveLoad(source interface{}, others ...interface{}) (*File, error) {
	return LoadSources(LoadOptions{Insensitive: true}, source, others...)
}

// Empty returns an empty file object.
func Empty() *File {
	// Ignore error here, we sure our data is good.
	f, _ := Load([]byte(""))
	return f
}

// NewSection creates a new section.
func (f *File) NewSection(name string) (*Section, error) {
	if len(name) == 0 {
		return nil, errors.New("error creating new section: empty section name")
	} else if f.options.Insensitive && name != DEFAULT_SECTION {
		name = strings.ToLower(name)
	}

	if f.BlockMode {
		f.lock.Lock()
		defer f.lock.Unlock()
	}

	if inSlice(name, f.sectionList) {
		return f.sections[name], nil
	}

	f.sectionList = append(f.sectionList, name)
	f.sections[name] = newSection(f, name)
	return f.sections[name], nil
}

// NewSections creates a list of sections.
func (f *File) NewSections(names ...string) (err error) {
	for _, name := range names {
		if _, err = f.NewSection(name); err != nil {
			return err
		}
	}
	return nil
}

// GetSection returns section by given name.
func (f *File) GetSection(name string) (*Section, error) {
	if len(name) == 0 {
		name = DEFAULT_SECTION
	} else if f.options.Insensitive {
		name = strings.ToLower(name)
	}

	if f.BlockMode {
		f.lock.RLock()
		defer f.lock.RUnlock()
	}

	sec := f.sections[name]
	if sec == nil {
		return nil, fmt.Errorf("section '%s' does not exist", name)
	}
	return sec, nil
}

// Section assumes named section exists and returns a zero-value when not.
func (f *File) Section(name string) *Section {
	sec, err := f.GetSection(name)
	if err != nil {
		// Note: It's OK here because the only possible error is empty section name,
		// but if it's empty, this piece of code won't be executed.
		sec, _ = f.NewSection(name)
		return sec
	}
	return sec
}

// Section returns list of Section.
func (f *File) Sections() []*Section {
	sections := make([]*Section, len(f.sectionList))
	for i := range f.sectionList {
		sections[i] = f.Section(f.sectionList[i])
	}
	return sections
}

// SectionStrings returns list of section names.
func (f *File) SectionStrings() []string {
	list := make([]string, len(f.sectionList))
	copy(list, f.sectionList)
	return list
}

func (f *File) reload(s dataSource) error {
	r, err := s.ReadCloser()
	if err != nil {
		return err
	}
	defer r.Close()

	return f.parse(r)
}

// Reload reloads and parses all data sources.
func (f *File) Reload() (err error) {
	for _, s := range f.dataSources {
		if err = f.reload(s); err != nil {
			// In loose mode, we create an empty default section for nonexistent files.
			if os.IsNotExist(err) && f.options.Loose {
				f.parse(bytes.NewBuffer(nil))
				continue
			}
			return err
		}
	}
	return nil
}

// Append appends one or more data sources and reloads automatically.
func (f *File) Append(source interface{}, others ...interface{}) error {
	ds, err := parseDataSource(source)
	if err != nil {
		return err
	}
	f.dataSources = append(f.dataSources, ds)
	for _, s := range others {
		ds, err = parseDataSource(s)
		if err != nil {
			return err
		}
		f.dataSources = append(f.dataSources, ds)
	}
	return f.Reload()
}
