package badstudent

// internal management of the steps of evaluation and correction

import (
	"github.com/pkg/errors"
)

type status_ int8

const (
	initialized  status_ = iota // 0
	checkOuts    status_ = iota // 1
	changed      status_ = iota // 2
	evaluated    status_ = iota // 3
	deltas       status_ = iota // 4
	adjusted     status_ = iota // 5
	weightsAdded status_ = iota // 6
)

// soon to be removed
const bias_value float64 = 1

// checks that the Layer (and none of its outputs) don't affect
// the outputs of the network
//
// returns error if l.isOutput == false but len(l.outputs) == 0
//
// recurses towards outputs
func (l *Layer) checkOutputs() error {
	l.statusMux.Lock()
	defer l.statusMux.Unlock()
	if l.status >= checkOuts {
		return nil
	}

	if len(l.outputs) == 0 && !l.isOutput {
		return errors.Errorf("Checked outputs; layer %v has no effect on network outputs (has no outputs and is not a network output)\n", l)
	}

	// remove the unused space at the end of slices
	{
		outputs := l.outputs
		l.outputs = make([]*Layer, len(outputs))
		copy(l.outputs, outputs)
	}

	for i, out := range l.outputs {
		if err := out.checkOutputs(); err != nil {
			return errors.Wrapf(err, "Checking outputs of output %d to layer %v (%v) failed\n", i, l, out)
		}
	}

	l.status = checkOuts
	return nil
}

// checks the outputs of all layers in the network
func (net *Network) checkOutputs() error {
	for i, in := range net.inLayers {
		if err := in.checkOutputs(); err != nil {
			return errors.Wrapf(err, "Failed to check outputs of network input %v (#%d)\n", in, i)
		}
	}

	return nil
}

// acts as a 'reset button' for the layer,
// signifying that it now will need to perform its operations again
//
// calls recursively on outputs
func (l *Layer) inputsChanged() {
	l.statusMux.Lock()
	if l.status < evaluated {
		l.statusMux.Unlock()
		return
	}

	l.status = changed
	l.statusMux.Unlock()

	for _, out := range l.outputs {
		out.inputsChanged()
	}
}

// sets the inputs of the network to the provided values
// returns an error if the length of the provided values doesn't
// match the size of the network inputs
func (net *Network) SetInputs(inputs []float64) error {
	if len(inputs) != len(net.inputs) {
		return errors.Errorf("Can't set inputs, len(inputs) != len(net.inputs) (%d != %d)", len(inputs), len(net.inputs))
	}

	copy(net.inputs, inputs)
	for _, in := range net.inLayers {
		in.inputsChanged()
	}

	return nil
}

// updates the values of the layer so that they are accurate, given the inputs
//
// calls recursively on inputs before running
func (l *Layer) evaluate() error {
	l.statusMux.Lock()
	defer l.statusMux.Unlock()
	if l.status >= evaluated && l.status != weightsAdded {
		return nil
	} else if len(l.inputs) == 0 {
		l.status = evaluated
		return nil
	}

	for i, in := range l.inputs {
		if err := in.evaluate(); err != nil {
			return errors.Wrapf(err, "Can't evaluate layer %v, evaluating input %v (#%d) failed\n", l, in, i)
		}
	}

	if err := l.typ.Evaluate(l, l.values); err != nil {
		return errors.Wrapf(err, "Couldn't evaluate layer %v, Operation evaluation failed\n", l)
	}

	l.status = evaluated
	return nil
}

// Returns a copy of the output values of the Network, given the inputs
//
// Returns an error if it can't the given inputs to be the network's
func (net *Network) GetOutputs(inputs []float64) ([]float64, error) {
	if err := net.SetInputs(inputs); err != nil {
		return nil, errors.Wrapf(err, "Couldn't get outputs; setting inputs failed.\n")
	}

	for i, out := range net.outLayers {
		if err := out.evaluate(); err != nil {
			return nil, errors.Wrapf(err, "Can't get outputs, network output layer %v (#%d) failed to evaluate\n", out, i)
		}
	}

	c := make([]float64, len(net.outputs))
	copy(c, net.outputs)
	return c, nil
}

// Calculates the deltas for each value of the layer
//
// Calls inputDeltas() on outputs in order to run (which in turn calls getDeltas())
// deltasMatter is: do the deltas of this layer actually need to be calculated, or should this
// just pass the recursion to its outputs
func (l *Layer) getDeltas(rangeCostDeriv func(int, int, func(int, float64)) error, deltasMatter bool) error {

	deltasMatter = deltasMatter || l.typ.CanBeAdjusted(l)

	l.statusMux.Lock()
	defer l.statusMux.Unlock()
	if l.status < evaluated {
		return errors.Errorf("Can't get deltas of layer %v, has not been evaluated", l)
	} else if l.status >= deltas && !(deltasMatter && !l.deltasActuallyCalculated) { // REWORK
		return nil
	}

	if !deltasMatter {
		for i, out := range l.outputs {
			if err := out.getDeltas(rangeCostDeriv, deltasMatter); err != nil { // deltasMatter = false
				return errors.Wrapf(err, "Can't pass on getting deltas from layer %v, getting deltas of layer %v (output %d) failed\n", l, out, i)
			}
		}
	} else {
		add := func(index int, addition float64) {
			l.deltas[index] += addition
		}

		// reset all of the values
		l.deltas = make([]float64, len(l.values))

		if l.isOutput {
			if err := rangeCostDeriv(0, len(l.values), add); err != nil {
				return errors.Wrapf(err, "Can't get deltas of output layer %v, rangeCostDeriv() failed\n", l)
			}
		}

		for i, out := range l.outputs {
			if err := out.inputDeltas(l, add, rangeCostDeriv); err != nil {
				return errors.Wrapf(err, "Can't get deltas of layer %v, input deltas from layer %v (output %d) failed\n", l, out, i)
			}
		}

		l.deltasActuallyCalculated = true
	}

	l.status = deltas
	return nil
}

// provides the deltas of each value to getDeltas()
//
// calls getDeltas() of self before running
func (l *Layer) inputDeltas(input *Layer, add func(int, float64), rangeCostDeriv func(int, int, func(int, float64)) error) error {
	l.statusMux.Lock()
	if l.status < evaluated {
		l.statusMux.Unlock()
		return errors.Errorf("Can't provide input deltas of layer %v (to %v), has not been evaluated", l, input)
	}

	if l.status < deltas {
		// unlock status so that getDeltas() can lock it
		l.statusMux.Unlock()

		if err := l.getDeltas(rangeCostDeriv, true); err != nil { // deltasMatter = true
			return errors.Wrapf(err, "Can't provide input deltas of layer %v (to %v), getting own deltas failed\n", l, input)
		}

		l.statusMux.Lock()
	}

	// find the index in 'l.inputs' that 'input' is. If not there, return error
	inputIndex := -1
	for i := range l.inputs {
		if l.inputs[i] == input {
			inputIndex = i
			break
		}
	}

	if inputIndex == -1 {
		return errors.Errorf("Can't provide input deltas of layer %v to %v, %v is not an input of %v", l, input, input, l)
	}

	start := l.PreviousInputs(inputIndex)
	end := start + l.InputSize(inputIndex)

	if err := l.typ.InputDeltas(l, add, start, end); err != nil {
		return errors.Wrapf(err, "Couldn't provide input deltas of layer %v to %v (#%d), Operator failed to get input deltas\n", l, input, inputIndex)
	}

	l.statusMux.Unlock()
	return nil
}

// recurses to inputs after running
// α
func (l *Layer) adjust(learningRate float64, saveChanges bool) error {
	l.statusMux.Lock()
	if l.status < deltas {
		l.statusMux.Unlock()
		return errors.Errorf("Can't adjust layer %v, has not calculated deltas", l)
	} else if l.status >= adjusted {
		l.statusMux.Unlock()
		return nil
	} else if l.inputs == nil {
		l.status = adjusted
		l.statusMux.Unlock()
		return nil
	}

	if err := l.typ.Adjust(l, learningRate, saveChanges); err != nil {
		return errors.Wrapf(err, "Couldn't adjust layer %v, Operator adjusting failed\n", l)
	}

	l.status = adjusted
	l.statusMux.Unlock()

	for i, in := range l.inputs {
		if err := in.adjust(learningRate, saveChanges); err != nil {
			return errors.Wrapf(err, "Failed to recurse after adjusting weights to layer %v (input %d) from layer %v\n", in, i, l)
		}
	}

	return nil
}

// recurses to inputs after running
func (l *Layer) addWeights() error {
	l.statusMux.Lock()
	if l.status >= weightsAdded {
		l.statusMux.Unlock()
		return nil
	}

	if err := l.typ.AddWeights(l); err != nil {
		return errors.Wrapf(err, "Couldn't add weights for layer %v, Operator failed to add weights\n", l)
	}

	l.status = weightsAdded
	l.statusMux.Unlock()

	for i, in := range l.inputs {
		if err := in.addWeights(); err != nil {
			return errors.Wrapf(err, "Failed to recurse to %v (input %d) after adding weights of layer %v\n", in, i, l)
		}
	}

	return nil
}

// Updates the weights in the newtork with any previously delayed changes
func (net *Network) AddWeights() error {

	for i, out := range net.outLayers {
		if err := out.addWeights(); err != nil {
			return errors.Wrapf(err, "Couldn't add weights of network, output layer %v (#%d) failed to add weights\n", out, i)
		}
	}

	return nil
}
