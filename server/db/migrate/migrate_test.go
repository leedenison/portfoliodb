package migrate

import (
	"testing"
	"testing/fstest"
)

func TestPluginAdvisoryLockID_Deterministic(t *testing.T) {
	a := pluginAdvisoryLockID("eodhd")
	b := pluginAdvisoryLockID("eodhd")
	if a != b {
		t.Errorf("non-deterministic: %d != %d", a, b)
	}
}

func TestPluginAdvisoryLockID_DiffersFromCore(t *testing.T) {
	id := pluginAdvisoryLockID("eodhd")
	if id == advisoryLockID {
		t.Error("plugin lock ID should differ from core lock ID")
	}
}

func TestPluginAdvisoryLockID_DiffersPerPlugin(t *testing.T) {
	a := pluginAdvisoryLockID("eodhd")
	b := pluginAdvisoryLockID("massive")
	if a == b {
		t.Errorf("different plugins should have different lock IDs: %d", a)
	}
}

func TestListMigrations_PluginFS(t *testing.T) {
	fs := fstest.MapFS{
		"001_init.sql":   {Data: []byte("CREATE TABLE t (id INT);")},
		"002_seed.sql":   {Data: []byte("INSERT INTO t VALUES (1);")},
		"readme.txt":     {Data: []byte("not a migration")},
		"003_extra.sql":  {Data: []byte("ALTER TABLE t ADD col TEXT;")},
	}

	names, err := listMigrations(fs)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 3 {
		t.Fatalf("got %d migrations, want 3", len(names))
	}
	want := []string{"001_init.sql", "002_seed.sql", "003_extra.sql"}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("names[%d] = %q, want %q", i, names[i], w)
		}
	}
}

func TestVersionFromName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"001_init.sql", "001"},
		{"099_foo_bar.sql", "099"},
		{"readme.txt", ""},
		{"1_short.sql", ""},
	}
	for _, tt := range tests {
		got := versionFromName(tt.name)
		if got != tt.want {
			t.Errorf("versionFromName(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}
