package ddmadapter

import (
	"context"
	"hash"
	"hash/fnv"
	"reflect"
	"testing"

	"github.com/jessepeterson/kmfddm/ddm"
	"github.com/jessepeterson/kmfddm/storage/inmem"
	"github.com/micromdm/nanomdm/mdm"
	"github.com/micromdm/nanomdm/test/enrollment"
	"github.com/valyala/fastjson"
)

// TestStatus verifies that we can both attach custom JSON mux handlers
// (parsers) and retrieve the built-in DDM parsed status report.
func TestStatus(t *testing.T) {
	// create a new DDM storage backend
	s := inmem.New(func() hash.Hash { return fnv.New128() })

	// create a new DDM adapter
	a, err := New(s, WithStatusIDFn(func(_ *mdm.Request, _ *ddm.StatusReport) (string, error) {
		return "testStatusID", nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	// make a new device
	e, err := enrollment.NewRandomDeviceEnrollment(nil, "com.example.test.topic", "/mdm", "/mdm")
	if err != nil {
		t.Fatal(err)
	}

	// create a status DDM check-in message
	msg := &mdm.DeclarativeManagement{
		Enrollment:  *e.GetEnrollment(),
		MessageType: mdm.MessageType{MessageType: "DeclarativeManagement"},
		Endpoint:    "status",
		Data: []byte(`{
    "test": true,
    "StatusItems": {
        "device": {
            "identifier": {
                "udid": "testUUID"
            }
        }
    }
}`),
	}

	ctx := context.Background()

	// create and "capture" the JSON mux
	ctx, mux := ContextJSONMux(ctx)

	var testVal bool

	// attach a custom parser to the JSON path muxer
	mux.HandleFunc(".test", func(path string, v *fastjson.Value) (unhandled []string, err error) {
		testVal, err = v.Bool()
		return
	})

	// create and "capture" the status report
	ctx, status := ContextStatusReport(ctx, msg.Data)

	// run the DDM endpoint
	r, err := a.DeclarativeManagement(e.NewMDMRequest(ctx), msg)
	if err != nil {
		t.Fatal(err)
	}

	if len(r) != 0 {
		// a DDM status check-in message should not return any data
		t.Error("non-zero length DM result")
	}

	// test that our custom JSON path mux handler ran
	if have, want := testVal, true; have != want {
		t.Errorf("have: %v, want: %v", have, want)
	}

	// the built-in DDM parser should have scraped this value
	testValues := []ddm.StatusValue{
		{Path: ".StatusItems.device.identifier.udid",
			ContainerType: "object",
			ValueType:     "string",
			Value:         []byte("testUUID"),
		},
	}

	// test that the parsed values are what we expect
	if have, want := status.Values, testValues; !reflect.DeepEqual(have, want) {
		t.Errorf("have: %v, want: %v", have, want)
	}

	// test that the status report got the test status ID
	if have, want := status.ID, "testStatusID"; have != want {
		t.Errorf("have: %v, want: %v", have, want)
	}
}
