package controller

import (
	gc "github.com/masayag/must-gather-operator/pkg/controller/garbagecollector"
	"github.com/masayag/must-gather-operator/pkg/controller/mustgatherreport"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerFuncs = append(AddToManagerFuncs, mustgatherreport.Add)
	AddToManagerFuncs = append(AddToManagerFuncs, gc.GC)
}
