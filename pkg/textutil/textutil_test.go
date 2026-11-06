package textutil

import (
	"reflect"
	"testing"
)

func TestParsePageRange(t *testing.T) {
	if got := ParsePageRange("1-3, 8, 10-9, 99", 10); !reflect.DeepEqual(got, []int{1, 2, 3, 8, 9, 10}) {
		t.Fatal(got)
	}
	if got := ParsePageRange("ทั้งหมด", 3); !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatal(got)
	}
}

func TestTabTitle(t *testing.T) {
	if got := TabTitle("คู่มือการใช้งานระบบ | เว็บทดสอบ"); got != "คู่มือการใช้งานระบบ" {
		t.Fatal(got)
	}
}
