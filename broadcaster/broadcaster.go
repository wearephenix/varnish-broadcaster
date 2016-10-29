package broadcasters

import "io"

type Broadcaster interface {
	HTTPMethod() string

	WriteToBuff([]byte)
	WriteToErrBuff([]byte)
	WriteTo(io.Writer)
	WriteErrTo(io.Writer)

}
