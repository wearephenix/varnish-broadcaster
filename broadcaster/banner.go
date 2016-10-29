package broadcasters

import "io"
import (
	"bytes"
	"github.com/mariusmagureanu/purger/dao"
)

type Banner struct {
	CacheGroup   dao.Group
	outputBuffer bytes.Buffer
	errBuffer    bytes.Buffer
}

func (b Banner) HTTPMethod() string {
	return "BAN"
}

func (b Banner) WriteToBuff(respOutput []byte) {
	b.outputBuffer.Write(respOutput)
}

func (b Banner) WriteToErrBuff(errOutput []byte) {
	b.errBuffer.Write(errOutput)
}

func (b Banner) WriteTo(out io.Writer) {
	out.Write(b.outputBuffer.Bytes())
}

func (b Banner) WriteErrTo(errOut io.Writer) {
	errOut.Write(b.errBuffer.Bytes())
}
