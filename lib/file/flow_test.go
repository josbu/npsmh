package file

import "testing"

func TestFlowAddAndSubAllowNilReceiver(t *testing.T) {
	var flow *Flow
	flow.Add(10, 20)
	flow.Sub(5, 6)
}

func TestFlowSubClampsNegativeValues(t *testing.T) {
	flow := &Flow{
		InletFlow:  3,
		ExportFlow: 4,
	}

	flow.Sub(10, 20)

	if flow.InletFlow != 0 || flow.ExportFlow != 0 {
		t.Fatalf("flow after Sub() = inlet=%d export=%d, want 0/0", flow.InletFlow, flow.ExportFlow)
	}
}
