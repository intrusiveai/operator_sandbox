//go:build linux || darwin

package campaignservice

import (
	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/schemas"
	"testing"
)

func TestInterfaceOperationDispatchCoverage(t *testing.T) {
	raw, err := schemas.Files.ReadFile(contracts.InterfaceDocument)
	if err != nil {
		t.Fatal(err)
	}
	pin, err := contracts.InterfaceIdentity(raw)
	if err != nil {
		t.Fatal(err)
	}
	p, err := contracts.LoadInterfaceProtocol(raw, pin)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{}
	for _, op := range p.Operations() {
		if service.operationHandler(op.Name) == nil {
			t.Errorf("published operation has no production handler: %s", op.Name)
		}
	}
	if service.operationHandler("engine.unknown") != nil {
		t.Fatal("unknown operation routed")
	}
}
