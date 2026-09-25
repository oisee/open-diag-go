package diag

import "testing"

func TestFieldIndexAndSet(t *testing.T) {
	atoms := []Atom{
		InputField(0, 34, 10, "300"),
		FieldName(0, 34, "P_MS", AttrYes3D),
		OutputField(2, 34, 10, "0", true),
		FieldName(2, 34, "P_TICKS", AttrProtected|AttrJustRight),
	}
	val := EncodeDyntAtoms(atoms)
	got, byName := FieldIndex(val)
	if byName["P_MS"] != 0 || byName["P_TICKS"] != 2 {
		t.Fatalf("index: %v", byName)
	}
	SetField(got, byName, "P_TICKS", "49")
	SetField(got, byName, "GHOST", "x") // ignored
	back, _ := ParseDyntAtoms(EncodeDyntAtoms(got))
	if back[2].Value() != "49" {
		t.Errorf("set: %q", back[2].Value())
	}
}

func TestFieldIndexDropdownAcrossPropertyBag(t *testing.T) {
	atoms := []Atom{
		{EType: AtomDropdown, Rest: []byte{0x00, 0x01}},
		{EType: AtomXMLProperty, Text: "<Propertybag/>"},
		FieldName(1, 20, "TRDIR-SUBC", 0),
		InputField(2, 20, 10, "unrelated"),
		{EType: AtomPushbutton, Text: "Save", Function: "=SAVE"},
		FieldName(3, 20, "NOT-A-FIELD", 0),
	}
	got, byName := FieldIndex(EncodeDyntAtoms(atoms))
	if got[0].TypeName() != "dropdown" || byName["TRDIR-SUBC"] != 0 {
		t.Fatalf("dropdown and its name were not paired: %v", byName)
	}
	if _, ok := byName["NOT-A-FIELD"]; ok {
		t.Fatalf("name crossed an unrelated button: %v", byName)
	}
}
