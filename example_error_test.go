package ecs_test

import (
	"errors"
	"fmt"

	"github.com/dradtke/ecs-go"
)

func FailingSystem() error {
	return errors.New("something went wrong")
}

func Example_failingSystem() {
	world := ecs.NewWorld()

	// By default system errors are logged using the log package, but we can
	// use this callback to add custom error-handling behavior.
	world.OnError = func(name string, args []interface{}, err error) {
		fmt.Printf(`system "%s" returned an error: %s`, name, err)
	}
	world.AddSystem(ecs.System{Func: FailingSystem})
	world.AddObject(ecs.NewObject())

	world.Run()

	// Output:
	// system "FailingSystem" returned an error: something went wrong
}
