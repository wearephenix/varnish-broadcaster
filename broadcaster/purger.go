package broadcasters

import "io"
import (
	"bytes"
	"github.com/mariusmagureanu/purger/dao"
	"fmt"
)

type Purger struct {
	CacheGroup   dao.Group
	outputBuffer bytes.Buffer
	errBuffer    bytes.Buffer
}

func (p Purger) HTTPMethod() string {
	return "PURGE"
}

func (p Purger) WriteToBuff(respOutput []byte) {
	p.outputBuffer.Write(respOutput)
}

func (p Purger) WriteToErrBuff(errOutput []byte) {
	p.errBuffer.Write(errOutput)
}

func (p Purger) WriteTo(out io.Writer) {
	out.Write(p.outputBuffer.Bytes())
	fmt.Println(p.outputBuffer.String())
}

func (p Purger) WriteErrTo(errOut io.Writer) {
	errOut.Write(p.errBuffer.Bytes())
	fmt.Println(p.errBuffer.String())
}
