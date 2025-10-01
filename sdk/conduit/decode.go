package conduit

import (
	"go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

func DecodeArg[A any](payload *common.Payload) (A, error) {
	dc := converter.GetDefaultDataConverter()
	var param A
	if err := dc.FromPayload(payload, &param); err != nil {
		return param, err
	}
	return param, nil
}
