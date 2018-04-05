package main

import (
	"github.com/sharnoff/smartlearning/badstudent"
	"github.com/sharnoff/smartlearning/badstudent/costfunctions"

	"fmt"
)

func main() {
	dataset := [][][]float64{
		{{-1, -1}, {0}},
		{{-1, 1}, {1}},
		{{1, -1}, {1}},
		{{1, 1}, {0}},
	}

	// these are the main adjustable variables
	learningRate := 1.0
	maxEpochs := 1000

	fmt.Println("Setting up network...")
	net := new(badstudent.Network)
	{
		var err error
		var l, hl *badstudent.Layer

		if l, err = net.Add("input", 2); err != nil {
			panic(err.Error())
		}

		if hl, err = net.Add("hidden layer neurons", 1, l); err != nil {
			panic(err.Error())
		}

		if l, err = net.Add("output neurons", 1, l, hl); err != nil {
			panic(err.Error())
		}

		if err = net.SetOutputs(l); err != nil {
			panic(err.Error())
		}
	}
	fmt.Println("Done!")

	res := make(chan struct {
		Avg, Percent  float64
		Epoch, IsTest bool
	})

	dataSrc, err := badstudent.TrainCh(dataset)
	if err != nil {
		panic(err.Error())
	}

	args := badstudent.TrainArgs{
		Data:     dataSrc,
		Results:  res,
		CostFunc: costfunctions.SquaredError(),
		Err:      &err,
	}

	fmt.Println("Starting training...")
	go net.Train(args, maxEpochs, learningRate)

	for r := range res {
		if r.Epoch {
			// fmt.Printf("Train → avg error: %v\t → percent correct: %v from EPOCH\n", r.Avg, r.Percent)
		} else { // it should never be a test, because we didn't give it testing data
			// fmt.Printf("Train → avg error: %v\t → percent correct: %v\n", r.Avg, r.Percent)
		}
	}

	if err != nil {
		panic(err.Error())
	}

	fmt.Println("Done training!")
}