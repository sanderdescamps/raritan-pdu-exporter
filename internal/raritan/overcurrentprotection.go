package raritan

import "github.com/tanenbaum/raritan-pdu-exporter/internal/rpc"

// Info for PDU OverCurrentProtector
type OCPInfo struct {
	Resource
	OCPMetadata
	OCPSettings
	Sensors
}

// OverCurrentProtectorMetadata metadata
type OCPMetadata struct {
	Label      string
	MaxTripCnt int
}

// OverCurrentProtectorSettings containing name
type OCPSettings struct {
	Name string
}

func (c *Client) GetOCPInfo() ([]OCPInfo, error) {
	ocps, err := c.GetPDUOCP()
	if err != nil {
		return nil, err
	}

	reqs := make([]bulkRequest, len(ocps)*3)
	for i, ocp := range ocps {
		i *= 3
		reqs[i] = bulkRequest{
			RID: ocp.RID,
			Request: rpc.Request{
				Method: "getMetaData",
			},
			Return: &OCPMetadata{},
		}
		reqs[i+1] = bulkRequest{
			RID: ocp.RID,
			Request: rpc.Request{
				Method: "getSettings",
			},
			Return: &OCPSettings{},
		}
		reqs[i+2] = bulkRequest{
			RID: ocp.RID,
			Request: rpc.Request{
				Method: "getSensors",
			},
			Return: &map[string]*Resource{},
		}
	}
	if _, err := c.bulkCall(reqs); err != nil {
		return nil, err
	}

	infos := make([]OCPInfo, len(ocps))
	for i, in := range ocps {
		j := i * 3
		meta := reqs[j].Return.(*OCPMetadata)
		sett := reqs[j+1].Return.(*OCPSettings)
		sens := reqs[j+2].Return.(*map[string]*Resource)
		infos[i] = OCPInfo{
			Resource:    in,
			OCPMetadata: *meta,
			OCPSettings: *sett,
			Sensors:     filterEmptySensors(*sens),
		}
	}
	return infos, nil
}
