package ini

import (
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestSection(t *testing.T) {
	Convey("Test CRD sections", t, func() {
		cfg, err := Load([]byte(_CONF_DATA), "testdata/conf.ini")
		So(err, ShouldBeNil)
		So(cfg, ShouldNotBeNil)

		Convey("Get section strings", func() {
			So(strings.Join(cfg.SectionStrings(), ","), ShouldEqual, "DEFAULT,author,package,package.sub,features,types,array,note,comments,advance")
		})

		Convey("Delete a section", func() {
			cfg.DeleteSection("")
			So(cfg.SectionStrings()[0], ShouldNotEqual, DEFAULT_SECTION)
		})

		Convey("Create new sections", func() {
			cfg.NewSections("test", "test2")
			_, err := cfg.GetSection("test")
			So(err, ShouldBeNil)
			_, err = cfg.GetSection("test2")
			So(err, ShouldBeNil)
		})
	})
}
