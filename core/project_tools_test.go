package core

import (
	"context"
	"testing"

	"crude/coreprotocol"
)

func TestProjectToolsNativeConsentCannotBeForgedByGenericControl(t *testing.T) {
	a := &actor{}
	for _, method := range []string{coreprotocol.ProjectToolsReview, coreprotocol.ProjectToolsDecide} {
		request := coreprotocol.Request{Method: method, Owner: coreprotocol.Owner{ClientID: "native-shell", Epoch: 1}, JSON: true}
		run, handled := a.prepareProjectTools(request)
		if !handled || run == nil {
			t.Fatal("native route is missing")
		}
		if result := run(context.Background()); result.Error == nil || result.Error.Code != "human_only" {
			t.Fatal("generic semantic request acquired native consent", result)
		}
		request.JSON, request.Owner = false, coreprotocol.Owner{}
		run, _ = a.prepareProjectTools(request)
		if result := run(context.Background()); result.Error == nil || result.Error.Code != "human_only" {
			t.Fatal("ownerless request acquired native consent", result)
		}
	}
	if run, handled := a.prepareProjectTools(coreprotocol.Request{Method: "code_tools.arbitrary"}); handled || run != nil {
		t.Fatal("setup introduced arbitrary method dispatch")
	}
}
