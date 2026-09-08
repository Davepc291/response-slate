package transcripteval

import (
	"bytes"
	"reflect"
	"testing"
)

func TestSharedDatasetContract(t *testing.T) {
	good := line(fixture("SYNTHETIC shared contract"))
	for _, data := range [][]byte{good, append(good, good...), []byte("{}"), []byte("\xff"), bytes.Replace(good, []byte(`"split":"train"`), []byte(`"split":"other"`), 1)} {
		_, evaluationError := evaluate(bytes.NewReader(data))
		parsed, e := DecodeRecords(data)
		if (e == nil) != (evaluationError == nil) {
			t.Fatal("validation drift")
		}
		records, _, withMetrics := DecodeDataset(data)
		if (withMetrics == nil) != (e == nil) || (e == nil && !reflect.DeepEqual(parsed, records)) {
			t.Fatal("shared decode drift")
		}
	}
}
