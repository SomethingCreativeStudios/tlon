package loadtest

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/SomethingCreativeStudios/tlon/catalog"
	recorddoc "github.com/SomethingCreativeStudios/tlon/internal/record"
	storeapi "github.com/SomethingCreativeStudios/tlon/store"
)

func TestRecordsAreDeterministicAcrossBatchBoundaries(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	all, err := Records(storeapi.StorageTemporal, 0, 20, 20, 42, start, 365)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Records(storeapi.StorageTemporal, 0, 7, 20, 42, start, 365)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Records(storeapi.StorageTemporal, 7, 13, 20, 42, start, 365)
	if err != nil {
		t.Fatal(err)
	}
	combined := append(first, second...)
	for index := range all {
		if all[index].ID != combined[index].ID || string(all[index].Document) != string(combined[index].Document) {
			t.Fatalf("record %d changed with batch boundaries", index)
		}
		derived, err := recorddoc.Derive(all[index].Document)
		if err != nil {
			t.Fatal(err)
		}
		if !derived.HasTime || derived.TimeStart == nil {
			t.Fatalf("temporal record %d has no closed start", index)
		}
	}
}

func TestMillionRecordRangeSpansConfiguredPeriod(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	first, err := Records(storeapi.StorageTemporal, 0, 1, 1_000_000, 42, start, 365*5)
	if err != nil {
		t.Fatal(err)
	}
	last, err := Records(storeapi.StorageTemporal, 999_999, 1, 1_000_000, 42, start, 365*5)
	if err != nil {
		t.Fatal(err)
	}
	firstTime, err := recorddoc.Derive(first[0].Document)
	if err != nil {
		t.Fatal(err)
	}
	lastTime, err := recorddoc.Derive(last[0].Document)
	if err != nil {
		t.Fatal(err)
	}
	if firstTime.TimeStart == nil || lastTime.TimeStart == nil {
		t.Fatal("generated range has no temporal bounds")
	}
	covered := lastTime.TimeStart.Sub(*firstTime.TimeStart)
	if covered < 4*365*24*time.Hour || covered > 6*365*24*time.Hour {
		t.Fatalf("million-record range covers %s, expected about five years", covered)
	}
}

func TestTransactionalRecordsExerciseCompleteTimeModel(t *testing.T) {
	records, err := Records(storeapi.StorageTransactional, 0, 10, 10, 42, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), 30)
	if err != nil {
		t.Fatal(err)
	}
	var missing, openStart, openEnd bool
	for _, input := range records {
		derived, err := recorddoc.Derive(input.Document)
		if err != nil {
			t.Fatal(err)
		}
		missing = missing || !derived.HasTime
		openStart = openStart || derived.HasTime && derived.TimeStart == nil
		openEnd = openEnd || derived.HasTime && derived.TimeStart != nil && derived.TimeEnd == nil
	}
	if !missing || !openStart || !openEnd {
		t.Fatalf("time coverage missing=%t openStart=%t openEnd=%t", missing, openStart, openEnd)
	}
}

func TestBundleNormalizesForBothStorageClasses(t *testing.T) {
	for _, class := range []string{storeapi.StorageTransactional, storeapi.StorageTemporal} {
		bundle, err := Bundle("load-"+class, class)
		if err != nil {
			t.Fatal(err)
		}
		normalized, _, err := catalog.Normalize(bundle)
		if err != nil {
			t.Fatalf("normalize %s: %v", class, err)
		}
		if normalized.Storage.Class != class || len(normalized.Queryables) < 10 || len(normalized.Facets) != 5 {
			t.Fatalf("normalized %s bundle = %#v", class, normalized)
		}
		if normalized.Storage.AutoFacetIndexes == nil || !*normalized.Storage.AutoFacetIndexes || len(normalized.Storage.Indexes) != 2 {
			t.Fatalf("normalized %s storage indexes = %#v", class, normalized.Storage)
		}
		var document map[string]any
		if err := json.Unmarshal(normalized.Catalog, &document); err != nil || document["itemType"] != "record" {
			t.Fatalf("catalog document = %#v, %v", document, err)
		}
	}
}

func TestOptionValidation(t *testing.T) {
	valid := defaults(Options{})
	if err := validateOptions(valid); err != nil {
		t.Fatal(err)
	}
	valid.Count = MaximumCount + 1
	if err := validateOptions(valid); err == nil {
		t.Fatal("oversized load accepted")
	}
}
