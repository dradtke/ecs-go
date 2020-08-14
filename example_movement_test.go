package ecs_test

import (
	"fmt"

	"github.com/dradtke/ecs-go"
)

type (
	Position int
	Velocity int
)

func Movement(p Position, v Velocity) Position {
	return Position(int(p) + int(v))
}

func Example_movement() {
	world := ecs.NewWorld()
	world.AddSystem(ecs.System{Func: Movement})

	player := ecs.NewObject(
		Position(1),
		Velocity(2),
	)
	world.AddObject(player)

	fmt.Println(player.Component(Position(0)))
	world.Run()
	fmt.Println(player.Component(Position(0)))

	// Output:
	// 1
	// 3
}
