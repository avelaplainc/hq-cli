package tree

import (
	"path/filepath"
	"strings"
	"testing"
)

// mustParse parses src or fails the test.
func mustParse(t *testing.T, src string) *Node {
	t.Helper()
	root, err := Parse([]byte(src), "test.hcl")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	return root
}

func TestParse(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		check func(t *testing.T, root *Node)
	}{
		{
			name: "nested block with labels",
			src:  `resource "aws_instance" "web" { ami = "ami-1" }`,
			check: func(t *testing.T, root *Node) {
				ami := root.Get("resource").Get("aws_instance").Get("web").Get("ami")
				if ami == nil || ami.Kind != KindString || ami.Str != "ami-1" {
					t.Fatalf("wrong nested value: %+v", ami)
				}
			},
		},
		{
			name: "label with dot stays one key",
			src:  `resource "aws.instance" "web" { a = 1 }`,
			check: func(t *testing.T, root *Node) {
				if root.Get("resource").Get("aws.instance").Get("web") == nil {
					t.Fatal("label with dot was split")
				}
			},
		},
		{
			name: "repeated block becomes array",
			src: `service "web" { port = 80 }
service "web" { port = 443 }`,
			check: func(t *testing.T, root *Node) {
				svc := root.Get("service").Get("web")
				if svc == nil || svc.Kind != KindArray || len(svc.Items) != 2 {
					t.Fatalf("want array of 2, got %+v", svc)
				}
				if got := svc.Items[1].Get("port").Num; got != "443" {
					t.Fatalf("wrong second item port: %v", got)
				}
			},
		},
		{
			name: "int number",
			src:  `count = 3`,
			check: func(t *testing.T, root *Node) {
				if n := root.Get("count"); n.Kind != KindNumber || n.Num != "3" {
					t.Fatalf("wrong int: %+v", n)
				}
			},
		},
		{
			name: "float number",
			src:  `ratio = 1.25`,
			check: func(t *testing.T, root *Node) {
				if n := root.Get("ratio"); n.Kind != KindNumber || n.Num != "1.25" {
					t.Fatalf("wrong float: %+v", n)
				}
			},
		},
		{
			name: "bools and null",
			src: `on = true
off = false
none = null`,
			check: func(t *testing.T, root *Node) {
				if b := root.Get("on"); b.Kind != KindBool || !b.Bool {
					t.Fatalf("wrong true: %+v", b)
				}
				if b := root.Get("off"); b.Kind != KindBool || b.Bool {
					t.Fatalf("wrong false: %+v", b)
				}
				if n := root.Get("none"); n.Kind != KindNull {
					t.Fatalf("wrong null: %+v", n)
				}
			},
		},
		{
			name: "tuple value",
			src:  `ports = [80, 443]`,
			check: func(t *testing.T, root *Node) {
				a := root.Get("ports")
				if a.Kind != KindArray || len(a.Items) != 2 || a.Items[0].Num != "80" {
					t.Fatalf("wrong tuple: %+v", a)
				}
			},
		},
		{
			name: "object value",
			src:  `meta = { a = 1, b = "x" }`,
			check: func(t *testing.T, root *Node) {
				o := root.Get("meta")
				if o.Kind != KindObject || len(o.Fields) != 2 {
					t.Fatalf("wrong object: %+v", o)
				}
				if o.Fields[0].Key != "a" || o.Fields[0].Val.Num != "1" {
					t.Fatalf("wrong object field a: %+v", o.Fields[0])
				}
			},
		},
		{
			name: "heredoc value",
			src:  "user_data = <<-EOF\n    #!/bin/bash\n    echo \"hi\"\n  EOF\n",
			check: func(t *testing.T, root *Node) {
				s := root.Get("user_data")
				if s.Kind != KindString || !strings.Contains(s.Str, "#!/bin/bash") {
					t.Fatalf("wrong heredoc: %+v", s)
				}
			},
		},
		{
			name: "interpolation falls back to the template text",
			src:  `ami = "ami-${var.x}"`,
			check: func(t *testing.T, root *Node) {
				s := root.Get("ami")
				if s.Kind != KindString || s.Str != `ami-${var.x}` {
					t.Fatalf("wrong fallback: %+v", s)
				}
			},
		},
		{
			name: "comparison keeps the whole source text",
			src:  `same = "pre" == "${var.suffix}"`,
			check: func(t *testing.T, root *Node) {
				s := root.Get("same")
				if s.Kind != KindString || s.Str != `"pre" == "${var.suffix}"` {
					t.Fatalf("wrong fallback: %+v", s)
				}
			},
		},
		{
			name: "variable ref falls back to source text",
			src:  `ami = var.base_ami`,
			check: func(t *testing.T, root *Node) {
				if s := root.Get("ami"); s.Str != "var.base_ami" {
					t.Fatalf("wrong fallback: %+v", s)
				}
			},
		},
		{
			name: "comment ignored",
			src: `# a comment
// another comment
a = 1 /* tail */`,
			check: func(t *testing.T, root *Node) {
				if len(root.Fields) != 1 || root.Fields[0].Key != "a" {
					t.Fatalf("comment leaked into fields: %+v", root.Fields)
				}
			},
		},
		{
			name: "source order around a block",
			src: `first = 1
block "x" { }
last = 2`,
			check: func(t *testing.T, root *Node) {
				want := []string{"first", "block", "last"}
				got := make([]string, len(root.Fields))
				for i, f := range root.Fields {
					got[i] = f.Key
				}
				if strings.Join(got, ",") != strings.Join(want, ",") {
					t.Fatalf("wrong order: %v", got)
				}
			},
		},
		{
			name: "marshal mixed tree",
			src: `a = 1
b = "x"
c = true
d = null
e = [1, 2]
svc "x" { p = 1.5 }`,
			check: func(t *testing.T, root *Node) {
				data, err := root.MarshalJSON()
				if err != nil {
					t.Fatal(err)
				}
				want := `{"a":1,"b":"x","c":true,"d":null,"e":[1,2],"svc":{"x":{"p":1.5}}}`
				if string(data) != want {
					t.Fatalf("wrong json: %s", data)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, mustParse(t, tt.src))
		})
	}
}

func TestParseCollisionError(t *testing.T) {
	tests := []struct {
		name string
		src  string
		key  string
	}{
		{"attribute after block", `foo "x" { a = 1 }
foo = 2`, "foo"},
		{"block after attribute", `foo = 2
foo "x" { a = 1 }`, "foo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.src), "collide.hcl")
			if err == nil {
				t.Fatal("want collision error, got nil")
			}
			for _, want := range []string{"collide.hcl", tt.key} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q misses %q", err, want)
				}
			}
		})
	}
}

func TestParseSyntaxError(t *testing.T) {
	_, err := Parse([]byte("a = = 1\n"), "bad.hcl")
	if err == nil {
		t.Fatal("want syntax error, got nil")
	}
	if !strings.Contains(err.Error(), "bad.hcl:1,") {
		t.Fatalf("error missing file and range: %v", err)
	}
}

func TestParseFile(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "sample.tf")
	root, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	// terraform and provider blocks exist.
	if root.Get("terraform").Get("required_providers").Get("aws") == nil {
		t.Fatal("missing terraform.required_providers.aws")
	}
	if root.Get("provider").Get("aws").Get("region").Str != "eu-west-1" {
		t.Fatal("missing provider.aws.region")
	}

	// two same-typed resources with different labels are distinct keys.
	instances := root.Get("resource").Get("aws_instance")
	web := instances.Get("web")
	db := instances.Get("db")
	if web == nil || db == nil {
		t.Fatalf("missing instances web/db: %+v", instances)
	}

	// values inside the web resource.
	if web.Get("instance_type").Str != "t3.micro" {
		t.Fatal("wrong web instance_type")
	}
	tags := web.Get("tags")
	if tags.Kind != KindObject || tags.Get("Name").Str != "web" {
		t.Fatalf("wrong tags: %+v", tags)
	}
	ports := web.Get("ports")
	if ports.Kind != KindArray || ports.Items[1].Num != "443" {
		t.Fatalf("wrong ports: %+v", ports)
	}
	if !strings.Contains(web.Get("user_data").Str, "#!/bin/bash") {
		t.Fatalf("wrong user_data: %+v", web.Get("user_data"))
	}

	// variable ref falls back to source text.
	if db.Get("ami").Str != "var.base_ami" {
		t.Fatalf("wrong db ami: %+v", db.Get("ami"))
	}
}

func TestParseFileMissing(t *testing.T) {
	_, err := ParseFile(filepath.Join("..", "..", "testdata", "missing.tf"))
	if err == nil {
		t.Fatal("want error for missing file")
	}
}

func TestGetMisses(t *testing.T) {
	root := mustParse(t, `a = 1`)
	if root.Get("b") != nil {
		t.Fatal("Get on absent key must return nil")
	}
	if root.Get("a").Get("x") != nil {
		t.Fatal("Get on scalar must return nil")
	}
	var nilNode *Node
	if nilNode.Get("a") != nil {
		t.Fatal("Get on nil must return nil")
	}
}
