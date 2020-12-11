package server

import (
	"sort"

	"github.com/nxjfxu/krhiza/internal/keys"
)

type Contact struct {
	Id      keys.Key
	Address string
}

type Contactable interface {
	GetId() []byte
	GetAddress() string
	GetPort() string
}

func sourceToContact(c Contactable) Contact {
	var id [keys.KeySize]byte
	copy(id[:], c.GetId())

	return Contact{id, c.GetAddress() + ":" + c.GetPort()}
}

func (c *Contact) Distance(k *keys.Key) ContactDistance {
	return ContactDistance{*c, *keys.Distance(&c.Id, k)}
}

type ContactDistance struct {
	contact  Contact
	distance keys.Key
}

type ByCloseness []ContactDistance

func (cs ByCloseness) Len() int           { return len(cs) }
func (cs ByCloseness) Swap(i, j int)      { cs[i], cs[j] = cs[j], cs[i] }
func (cs ByCloseness) Less(i, j int) bool { return keys.Le(&cs[i].distance, &cs[j].distance) }
func (cs *ByCloseness) Push(x interface{}) {
	*cs = append(*cs, x.(ContactDistance))
}
func (cs *ByCloseness) Pop() interface{} {
	l := len(*cs)
	last := (*cs)[l-1]
	*cs = (*cs)[:l-1]
	return last
}

type ByDistance []ContactDistance

func (cs ByDistance) Len() int           { return len(cs) }
func (cs ByDistance) Swap(i, j int)      { cs[i], cs[j] = cs[j], cs[i] }
func (cs ByDistance) Less(i, j int) bool { return !keys.Le(&cs[i].distance, &cs[j].distance) }
func (cs *ByDistance) Push(x interface{}) {
	*cs = append(*cs, x.(ContactDistance))
}
func (cs *ByDistance) Pop() interface{} {
	l := len(*cs)
	last := (*cs)[l-1]
	*cs = (*cs)[:l-1]
	return last
}

type RoutingTree interface {
	Depth() int
	Prefix() *keys.Key
	Size() int
	K() int

	// Returns all contacts
	Contacts() []Contact

	// Returns the k contacts closest to key,
	// sorted by ascending distance
	ClosestK(k int, key *keys.Key) []ContactDistance

	AddContact(sId *keys.Key, c *Contact) (RoutingTree, bool)
	RemoveContact(key *keys.Key) RoutingTree
}

type RoutingTreeBranch struct {
	depth  int
	prefix keys.Key
	k      int

	zero RoutingTree
	one  RoutingTree
}

func (t RoutingTreeBranch) Depth() int {
	return t.depth
}

func (t RoutingTreeBranch) Prefix() *keys.Key {
	return &t.prefix
}

func (t RoutingTreeBranch) K() int {
	return t.k
}

func (t RoutingTreeBranch) Size() int {
	return t.zero.Size() + t.one.Size()
}

func (t RoutingTreeBranch) Zero() RoutingTree {
	return t.zero
}

func (t RoutingTreeBranch) One() RoutingTree {
	return t.one
}

func (t RoutingTreeBranch) Contacts() []Contact {
	c0 := t.zero.Contacts()
	c1 := t.one.Contacts()
	return append(c0, c1...)
}

func (t RoutingTreeBranch) ClosestK(k int, key *keys.Key) []ContactDistance {
	c0 := t.zero.ClosestK(k, key)
	c1 := t.one.ClosestK(k, key)

	l := len(c0) + len(c1)
	if l > k {
		l = k
	}

	result := make([]ContactDistance, l)
	for i, i0, i1 := 0, 0, 0; i < l && (i0 < len(c0) || i1 < len(c1)); i++ {
		if i0 == len(c0) {
			result[i] = c1[i1]
			i1++
		} else if i1 == len(c1) {
			result[i] = c0[i0]
			i0++
		} else {
			if keys.Le(&c0[i0].distance, &c1[i1].distance) {
				result[i] = c0[i0]
				i0++
			} else {
				result[i] = c1[i1]
				i1++
			}
		}
	}

	return result
}

func (t RoutingTreeBranch) AddContact(sId *keys.Key, c *Contact) (RoutingTree, bool) {
	var zero RoutingTree
	var one RoutingTree
	change := false
	d := t.depth

	b := keys.Bit(&c.Id, d)
	if b == 0 {
		zero, change = t.zero.AddContact(sId, c)
		one = t.one
	} else {
		zero = t.zero
		one, change = t.one.AddContact(sId, c)
	}

	return RoutingTreeBranch{d, t.prefix, t.k, zero, one}, change
}

func (t RoutingTreeBranch) RemoveContact(key *keys.Key) RoutingTree {
	zero := t.zero.RemoveContact(key)
	one := t.one.RemoveContact(key)
	return RoutingTreeBranch{t.depth, t.prefix, t.k, zero, one}
}

type RoutingTreeLeaf struct {
	depth  int
	prefix keys.Key
	k      int

	kBucket []Contact
}

func (t RoutingTreeLeaf) Split() *RoutingTreeBranch {
	d := t.depth
	prefix := t.prefix
	var zeros []Contact
	var ones []Contact

	for i := 0; i < len(t.kBucket); i++ {
		c := t.kBucket[i]
		b := keys.Bit(&c.Id, d)
		if b == 0 {
			zeros = append(zeros, c)
		} else if b == 1 {
			ones = append(ones, c)
		}
	}

	zero := RoutingTreeLeaf{d + 1, keys.SetBit(&prefix, d, 0), t.k, zeros}
	one := RoutingTreeLeaf{d + 1, keys.SetBit(&prefix, d, 1), t.k, ones}
	return &RoutingTreeBranch{t.depth, prefix, t.K(), zero, one}
}

// RoutingTree methods

func (t RoutingTreeLeaf) Depth() int {
	return t.depth
}

func (t RoutingTreeLeaf) Prefix() *keys.Key {
	return &t.prefix
}

func (t RoutingTreeLeaf) K() int {
	return t.k
}

func (t RoutingTreeLeaf) Size() int {
	return len(t.kBucket)
}

func (t RoutingTreeLeaf) Contacts() []Contact {
	return t.kBucket
}

func (t RoutingTreeLeaf) ClosestK(k int, key *keys.Key) []ContactDistance {
	l := len(t.kBucket)
	if k < l {
		l = k
	}

	cds := make([]ContactDistance, 0, len(t.kBucket))
	for _, c := range t.kBucket {
		cds = append(cds, c.Distance(key))
	}
	sort.Sort(ByCloseness(cds))

	return cds
}

func (t RoutingTreeLeaf) AddContact(sId *keys.Key, c *Contact) (RoutingTree, bool) {
	for _, c0 := range t.kBucket {
		if c0 == *c {
			return t, false
		}
	}

	d := t.depth
	if len(t.kBucket) < t.K() {
		for _, ec := range t.kBucket {
			if ec.Id == c.Id {
				return t, false
			}
		}
		t.kBucket = append(t.kBucket, *c)
		return t, true
	} else {
		// If this leaf is full
		if keys.Includes(&t.prefix, sId, d) {
			// If this node is included in t.prefix, split
			newTree, change := t.Split().AddContact(sId, c)
			return newTree, change
		} else {
			// If not we can finish
			return t, false
		}
	}
}

func (t RoutingTreeLeaf) RemoveContact(key *keys.Key) RoutingTree {
	toRemove := -1
	for i, c := range t.kBucket {
		if c.Id == *key {
			toRemove = i
			break
		}
	}

	if toRemove != -1 {
		t.kBucket = append(t.kBucket[:toRemove], t.kBucket[toRemove+1:]...)
	}
	return t
}
