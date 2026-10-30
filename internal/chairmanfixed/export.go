package chairmanfixed

// Node is one path of an assembled image's merged filesystem: Type is 'f', 'l'
// or 'd'; Mode is the permission and special bits.
type Node struct {
	Type     byte
	Mode     int
	UID, GID int
	Link     string
}

// Layout is the merged filesystem of one image and the final content of the
// account files, which the other bases' validation reads.
type Layout struct {
	Files    map[string]Node
	Accounts map[string]string // "etc/passwd", "etc/group"
}

// ReadLayout merges the layers of the one image in the OCI layout at dir, without
// running anything in it.
func ReadLayout(dir string) (Layout, error) {
	img, err := readImage(dir, nil, map[string]bool{"etc/passwd": true, "etc/group": true})
	if err != nil {
		return Layout{}, err
	}
	l := Layout{Files: make(map[string]Node, len(img.files)), Accounts: img.final}
	for p, e := range img.files {
		l.Files[p] = Node{Type: e.typ, Mode: e.mode, UID: e.uid, GID: e.gid, Link: e.link}
	}
	return l, nil
}
