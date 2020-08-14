package ecs_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/dradtke/ecs-go"
)

type (
	Player struct{}
	Target struct{}
)

// like a time.Ticker channel, but only ticks n times and then closes
func MaxTicker(d time.Duration, n int) <-chan time.Time {
	ch := make(chan time.Time)
	go func() {
		ticker := time.NewTicker(d)
		for i := 0; i < n; i++ {
			ch <- (<-ticker.C)
		}
		ticker.Stop()
		close(ch)
	}()
	return ch
}

func TestMovement(t *testing.T) {
	movement := func(p Position, v Velocity) Position {
		return Position(int(p) + int(v))
	}

	t.Run("once", func(t *testing.T) {
		world := ecs.NewWorld()
		world.AddSystem(ecs.System{Func: movement})

		player := ecs.NewObject(Position(1), Velocity(2))
		world.AddObject(player)

		world.Run()
		if p := player.Component(Position(0)).(Position); p != Position(3) {
			t.Errorf("bad position: want 3, got %v", p)
		}
	})

	t.Run("three times", func(t *testing.T) {
		world := ecs.NewWorld()
		world.AddSystem(ecs.System{
			Func:   movement,
			Ticker: MaxTicker(100*time.Millisecond, 3),
		})

		player := ecs.NewObject(Position(1), Velocity(2))
		world.AddObject(player)

		world.Run()
		if got, want := player.Component(Position(0)).(Position), 7; got != Position(want) {
			t.Errorf("bad position: got %v, want %v", got, want)
		}
	})
}

func TestTime(t *testing.T) {
	ticker := make(chan time.Time)
	go func() {
		ticker <- time.Unix(100, 0)
		ticker <- time.Unix(200, 0)
		close(ticker)
	}()

	times := make([]time.Time, 0, 2)
	saveTime := func(now time.Time) {
		times = append(times, now)
	}

	world := ecs.NewWorld()
	world.AddObject(ecs.NewObject())
	world.AddSystem(ecs.System{
		Func:   saveTime,
		Ticker: ticker,
	})

	world.Run()

	if got, want := len(times), 2; got != want {
		t.Fatalf("wrong number of times: got %d, want %d", got, want)
	}
	if got, want := times[0], time.Unix(100, 0); got != want {
		t.Errorf("wrong time: got %s, want %s", got, want)
	}
	if got, want := times[1], time.Unix(200, 0); got != want {
		t.Errorf("wrong time: got %s, want %s", got, want)
	}
}

func TestError(t *testing.T) {
	cannotMove := errors.New("cannot move")

	movement := func(p Position, v Velocity) (Position, error) {
		return Position(0), cannotMove
	}

	var onErrorInvoked bool

	world := ecs.NewWorld()
	world.OnError = func(name string, args []interface{}, err error) {
		onErrorInvoked = true
		if got, want := name, "movement"; got != want {
			t.Errorf("received bad name: got %s, want %s", got, want)
		}
		if got, want := args, []interface{}{Position(1), Velocity(2)}; !reflect.DeepEqual(got, want) {
			t.Errorf("received bad args: got %v, want %v", got, want)
		}
		if got, want := err, cannotMove; got != want {
			t.Errorf("received bad error: got %s, want %s", got, want)
		}
	}

	world.AddObject(ecs.NewObject(Position(1), Velocity(2)))
	world.AddSystem(ecs.System{Func: movement, Name: "movement"})

	world.Run()

	if !onErrorInvoked {
		t.Error("OnError not invoked")
	}
}

func TestMoveTowardsTarget(t *testing.T) {
	world := ecs.NewWorld()
	player := ecs.NewObject(Player{}, Position(1))
	world.AddObject(player)
	world.AddObject(ecs.NewObject(Target{}, Position(5)))

	movement := func(_ Player, pos Position, targetIter func() (Target, Position, bool)) Position {
		_, targetPos, ok := targetIter()
		if !ok {
			t.Fatal("failed to find target")
		}
		if pos < targetPos {
			return pos + 1
		}
		return pos
	}

	world.AddSystem(ecs.System{
		Func:   movement,
		Ticker: time.NewTicker(100 * time.Millisecond).C,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	world.RunContext(ctx)

	if got, want := player.Component(Position(0)).(Position), Position(5); got != want {
		t.Errorf("player didn't move towards target: got %v, want %v", got, want)
	}
}

func TestQueryAll(t *testing.T) {
	world := ecs.NewWorld()
	world.AddObject(ecs.NewObject(Player{}))

	targets := make(map[ecs.Entity]struct{})
	for i := 0; i < 5; i++ {
		target := ecs.NewObject(Target{})
		targets[world.AddObject(target)] = struct{}{}
	}

	var findTargetsInvoked bool

	findTargets := func(_ Player, targetIter func(int) (int, ecs.Entity, Target, bool)) {
		findTargetsInvoked = true

		// iterate over all available objects with the Target component
		for i, entity, _, ok := targetIter(0); ok; i, entity, _, ok = targetIter(i + 1) {
			delete(targets, entity)
		}

		if len(targets) > 0 {
			t.Error("not all targets were found")
		}
	}

	world.AddSystem(ecs.System{Func: findTargets})

	world.Run()

	if !findTargetsInvoked {
		t.Error("findTargets not invoked")
	}
}

func TestAddAndRemoveComponents(t *testing.T) {
	player := ecs.NewObject(Position(1))

	if player.Component(Velocity(0)) != nil {
		t.Fatal("player should not have velocity")
	}

	player.AddComponent(Velocity(2))
	if player.Component(Velocity(0)) == nil {
		t.Fatal("player should have velocity")
	}

	player.RemoveComponent(Velocity(0))
	if player.Component(Velocity(0)) != nil {
		t.Fatal("player should not have velocity")
	}
}

func TestAddAndRemoveObjects(t *testing.T) {
	world := ecs.NewWorld()

	player := world.AddObject(ecs.NewObject())
	if world.GetObject(player) == nil {
		t.Fatal("player should exist")
	}

	world.RemoveObject(player)
	if world.GetObject(player) != nil {
		t.Fatal("player should not exist")
	}
}

func TestSelfDestruct(t *testing.T) {
	type Marked struct{}

	// Systems can take a few special types, including *ecs.World and ecs.Entity,
	// in order to modify the world.
	selfDestruct := func(world *ecs.World, entity ecs.Entity, _ Marked) {
		world.RemoveObject(entity)
	}

	world := ecs.NewWorld()
	willDestruct := world.AddObject(ecs.NewObject(Marked{}))
	willNotDestruct := world.AddObject(ecs.NewObject(Marked{}))
	world.AddSystem(ecs.System{Func: selfDestruct})

	world.Run()

	if world.GetObject(willDestruct) != nil {
		t.Error("expected object to self-destruct")
	}
	if world.GetObject(willNotDestruct) != nil {
		t.Error("expected object not to self-destruct")
	}
}
