package http_to_kitex

import (
	"context"
	"fmt"

	"github.com/cloudwego/gopkg/bufiox"
	"github.com/cloudwego/gopkg/protocol/thrift"
	"github.com/cloudwego/kitex/pkg/generic"
	"github.com/cloudwego/kitex/pkg/remote"
	thriftcodec "github.com/cloudwego/kitex/pkg/remote/codec/thrift"
	"github.com/cloudwego/kitex/pkg/transmeta"
)

// Kitex v0.16.4's DynamicGo HTTP reader assumes a success field exists.
// TTHeader business errors may carry only a STOP byte, which makes that reader
// allocate a negative-sized buffer. Keep normal Thrift message validation, but
// consume business-error results without running the success-body converter.
// ClientTTHeaderHandler still supplies the original BizStatusError to the caller.
type businessErrorCodec struct{ remote.PayloadCodec }

func newBusinessErrorCodec() remote.PayloadCodec {
	return businessErrorCodec{PayloadCodec: thriftcodec.NewThriftCodec()}
}

func (c businessErrorCodec) Unmarshal(ctx context.Context, msg remote.Message, in remote.ByteBuffer) error {
	if msg.RPCRole() == remote.Client {
		bizErr, err := transmeta.ParseBizStatusErr(msg.TransInfo().TransStrInfo())
		if err != nil {
			return err
		}
		if bizErr != nil {
			result, ok := msg.Data().(generic.WithCodec)
			if !ok {
				return fmt.Errorf("Kitex business error has no generic result")
			}
			result.SetCodec(businessErrorReader{})
		}
	}
	return c.PayloadCodec.Unmarshal(ctx, msg, in)
}

type businessErrorReader struct{}

func (businessErrorReader) Read(_ context.Context, _ string, _ bool, _ int, in bufiox.Reader) (interface{}, error) {
	reader := thrift.NewBufferReader(in)
	defer reader.Recycle()
	return nil, reader.Skip(thrift.STRUCT)
}
