package config

import (
	"reflect"
	"sync"
	"sync/atomic"
)

// Holder owns the process-wide config and hands out immutable snapshots.
//
// A hot reload swaps the pointer rather than overwriting the struct in place,
// so a reader either sees the whole old config or the whole new one, never a
// mix. Readers call Get once and read every field off the returned snapshot;
// they must not mutate it. Writers go through Update, which clones under a
// mutex so concurrent read-modify-writes cannot lose each other's edits.
//
// The zero Holder is not usable; construct one with NewHolder.
type Holder struct {
	ptr atomic.Pointer[Config]
	mu  sync.Mutex // serialises Update's read-modify-write
}

// NewHolder returns a Holder wrapping cfg. A nil cfg is allowed: Get then
// returns nil, matching the nil-config checks callers already make.
func NewHolder(cfg *Config) *Holder {
	h := &Holder{}
	h.ptr.Store(cfg)
	return h
}

// Get returns the current snapshot. Nil-safe on a nil Holder so callers that
// treat a missing config as "feature unavailable" keep working.
func (h *Holder) Get() *Config {
	if h == nil {
		return nil
	}
	return h.ptr.Load()
}

// Store publishes cfg as the new snapshot, discarding the previous one. Used
// by the hot reload, which builds its config from disk rather than from the
// current snapshot.
func (h *Holder) Store(cfg *Config) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ptr.Store(cfg)
}

// Update clones the current snapshot, applies fn to the clone, and publishes
// it. It returns the published snapshot, or nil if the Holder is nil or holds
// no config. fn must not retain the pointer it is given.
func (h *Holder) Update(fn func(*Config)) *Config {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	cur := h.ptr.Load()
	if cur == nil {
		return nil
	}
	next := cur.Clone()
	fn(next)
	h.ptr.Store(next)
	return next
}

// Clone returns a deep copy of the config. Config is plain TOML-decodable data
// — structs, slices, maps, and pointers to scalars — so a reflective copy is
// both correct and immune to new fields being added without maintenance.
func (c *Config) Clone() *Config {
	if c == nil {
		return nil
	}
	out := new(Config)
	deepCopy(reflect.ValueOf(out).Elem(), reflect.ValueOf(c).Elem())
	return out
}

// deepCopy copies src into dst, allocating fresh backing storage for every
// slice, map, and pointer so the copy shares no mutable state with the source.
func deepCopy(dst, src reflect.Value) {
	switch src.Kind() {
	case reflect.Pointer:
		if src.IsNil() {
			return
		}
		p := reflect.New(src.Type().Elem())
		deepCopy(p.Elem(), src.Elem())
		dst.Set(p)
	case reflect.Slice:
		if src.IsNil() {
			return
		}
		s := reflect.MakeSlice(src.Type(), src.Len(), src.Len())
		for i := 0; i < src.Len(); i++ {
			deepCopy(s.Index(i), src.Index(i))
		}
		dst.Set(s)
	case reflect.Map:
		if src.IsNil() {
			return
		}
		m := reflect.MakeMapWithSize(src.Type(), src.Len())
		iter := src.MapRange()
		for iter.Next() {
			v := reflect.New(src.Type().Elem()).Elem()
			deepCopy(v, iter.Value())
			m.SetMapIndex(iter.Key(), v)
		}
		dst.Set(m)
	case reflect.Struct:
		for i := 0; i < src.NumField(); i++ {
			if !dst.Field(i).CanSet() { // unexported; config has none, but be safe
				continue
			}
			deepCopy(dst.Field(i), src.Field(i))
		}
	case reflect.Interface:
		if src.IsNil() {
			return
		}
		v := reflect.New(src.Elem().Type()).Elem()
		deepCopy(v, src.Elem())
		dst.Set(v)
	default:
		dst.Set(src)
	}
}
