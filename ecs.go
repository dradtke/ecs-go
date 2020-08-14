// Package ecs implements an Entity Component System library.
//
// Loosely inspired by the bevy ECS library for Rust, this implementation
// attempts to achieve something similar in Go. Each system is defined as a
// function that accepts as input any relevant components, which are identified
// by their type:
//
//     type (
//         Position float32
//         Velocity float32
//     )
//
//     func Movement(pos Position, vel Velocity) Position {
//         return Position(float32(pos) + float32(vel))
//     }
//
// The Position and Velocity types represent components, and the Movement
// system will be invoked on every object that contains an instance of both.
// Because it returns a Position, the object's matching component will be
// overriden with the return value, effectively updating the object's Position
// component on each iteration.
//
// For the most part, system method signatures will be defined by which components they operate on, and which ones they update. However, there are two special constructs that can be used as well:
//
//
//     // Systems can return an error as their final return value which are
//     // handled by the world's error handler.
//     func Movement(pos Position, vel Velocity) (Position, error) {
//         // ...
//     }

//     func Movement(now time.Time, pos Position, vel Velocity) Position {
//         // ...
//     }
package ecs

import (
	"context"
	"errors"
	"log"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

var (
	gid        uint64 = 0
	errorType         = reflect.TypeOf((*error)(nil)).Elem()
	timeType          = reflect.TypeOf(time.Time{})
	entityType        = reflect.TypeOf(Entity(0))
	intType           = reflect.TypeOf(int(0))
)

type World struct {
	// OnError is a callback that will be invoked when a system returns an error as its final argument.
	OnError func(name string, args []interface{}, err error)

	objects []*Object
	systems []System
}

func NewWorld() *World {
	return &World{
		objects: make([]*Object, 0),
		systems: make([]System, 0),
	}
}

func (w *World) AddObject(ob *Object) Entity {
	w.objects = append(w.objects, ob)
	return ob.entity
}

func (w *World) GetObject(entity Entity) *Object {
	for _, ob := range w.objects {
		if ob.entity == entity {
			return ob
		}
	}
	return nil
}

func (w *World) AddSystem(s System) {
	w.systems = append(w.systems, s)
}

func (w *World) Run() {
	w.RunContext(context.Background())
}

func (w *World) RunContext(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(len(w.systems))

	for _, s := range w.systems {
		go func(s System) {
			s.run(ctx, w)
			wg.Done()
		}(s)
	}

	wg.Wait()
}

func (w *World) handleSystemError(name string, args []interface{}, err error) {
	if w.OnError != nil {
		(w.OnError)(name, args, err)
		return
	}

	log.Printf(`system "%s" returned error: %s`, name, err)
}

func (w *World) makeObjectIter(t reflect.Type) (reflect.Value, error) {
	if t.NumIn() > 1 {
		return reflect.Value{}, errors.New("invalid signature: at most one argument expected")
	}

	if t.NumOut() < 2 {
		return reflect.Value{}, errors.New("invalid signature: at least two return values expected")
	}
	if t.Out(t.NumOut()-1).Kind() != reflect.Bool {
		return reflect.Value{}, errors.New("invalid signature: last return value must be a boolean")
	}

	return reflect.MakeFunc(t, func(args []reflect.Value) (results []reflect.Value) {
		results = make([]reflect.Value, t.NumOut())

		start := 0
		if t.NumIn() == 1 && t.In(0).Kind() == reflect.Int {
			start = args[0].Interface().(int)
		}

	ol:
		for i, ob := range w.objects[start:] {
			for out := 0; out < t.NumOut()-1; out++ {
				ot := t.Out(out)
				if ot == intType {
					results[out] = reflect.ValueOf(i + start)
				} else if ot == entityType {
					results[out] = reflect.ValueOf(ob.entity)
				} else {
					c := ob.getComponent(ot)
					if !c.IsValid() {
						continue ol
					}
					results[out] = c
				}
			}

			// object found
			results[len(results)-1] = reflect.ValueOf(true)
			return results
		}

		for out := 0; out < t.NumOut(); out++ {
			ot := t.Out(out)
			if ot == intType {
				results[out] = reflect.ValueOf(len(w.objects))
			} else {
				results[out] = reflect.Zero(ot)
			}
		}
		return results
	}), nil
}

type Entity uint64

type Object struct {
	entity     Entity
	components []interface{}
}

func NewObject(cs ...interface{}) *Object {
	return &Object{
		entity:     Entity(atomic.AddUint64(&gid, 1)),
		components: cs,
	}
}

func (o *Object) Entity() Entity {
	return o.entity
}

func (o *Object) Components() []interface{} {
	return o.components
}

func (o *Object) getComponent(t reflect.Type) reflect.Value {
	for _, c := range o.components {
		v := reflect.ValueOf(c)
		if v.Type() == t {
			return v
		}
	}
	return reflect.Value{}
}

type System struct {
	Func   interface{}
	Name   string
	Ticker <-chan time.Time
}

func (s System) run(ctx context.Context, w *World) error {
	if s.Ticker == nil {
		s.tick(w, time.Now())
		return nil
	}

	// TODO: add a "pause" capability
	for {
		select {
		case now, ok := <-s.Ticker:
			if !ok {
				return nil
			}
			s.tick(w, now)

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s System) tick(w *World, now time.Time) {
	f := reflect.ValueOf(s.Func)

	argTypes := make([]reflect.Type, f.Type().NumIn())
	argValues := make([]reflect.Value, f.Type().NumIn())
	for i := 0; i < f.Type().NumIn(); i++ {
		argTypes[i] = f.Type().In(i)
	}

ol:
	for _, ob := range w.objects {
	tl:
		for i, t := range argTypes {
			if t == timeType {
				argValues[i] = reflect.ValueOf(now)
				continue tl
			}

			if t.Kind() == reflect.Func {
				// anything to do if the func takes arguments?
				var err error
				if argValues[i], err = w.makeObjectIter(t); err != nil {
					log.Printf("failed to make object iter: %s", err)
					continue ol
				} else {
					continue tl
				}
			}

			for _, c := range ob.components {
				cv := reflect.ValueOf(c)
				if cv.Type().AssignableTo(t) {
					argValues[i] = cv // need to convert?
					continue tl
				}
			}

			// skipping this object because it doesn't have the required components
			continue ol
		}

		results := f.Call(argValues)
		if len(results) == 0 {
			continue ol
		}

		if v := results[len(results)-1]; v.Type() == errorType {
			results = results[:len(results)-1]
			if !v.IsNil() {
				name := s.Name
				if name == "" {
					name = runtime.FuncForPC(f.Pointer()).Name()
				}
				args := make([]interface{}, len(argValues))
				for i, v := range argValues {
					args[i] = v.Interface()
				}
				err := v.Interface().(error)
				w.handleSystemError(name, args, err)
			}
		}

	rl:
		for _, result := range results {
			for i, c := range ob.components {
				cv := reflect.ValueOf(c)
				if result.Type().AssignableTo(cv.Type()) {
					// TODO: finish
					reflect.ValueOf(ob.components).Index(i).Set(result)
					continue rl
				}
			}
		}
	}
}
