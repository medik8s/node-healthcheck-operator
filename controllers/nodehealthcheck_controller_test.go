package controllers

import (
	. "github.com/onsi/ginkgo"
	//. "github.com/onsi/gomega"
)

var _ = Describe("Node Health Check CR", func() {
	Context("Defaults", func() {
		When("creating a resource with only external template specified", func() {
			It("sets a default condition Unready 300s", func() {

			})
			It("sets max unhealthy to 49%", func() {

			})
			It("sets unhealthy to 49%", func() {

			})
			It("sets an empty selector to select all nodes", func() {

			})
		})
	})
	Context("Validation", func() {
		When("specifying an external remediation template", func() {
			It("should fail creation if empty", func() {

			})
			It("should fail creation malformed", func() {

			})
			It("should succeed creation with valid object format", func() {

			})
			It("should succeed creation even with a non-existing template", func() {

			})
		})
		When("specifying a selector", func() {
			It("fails creation on wrong selector input", func() {

			})
			It("succeeds creation on valid selector input", func() {

			})
		})
		When("specifying unhealthy conditions", func() {
			It("fails creation on wrong unhealthy conditions input", func() {

			})
			It("succeeds creation on valid unhealthy condition input", func() {

			})
		})
		When("specifying max unhealthy", func() {
			It("fails creation on percentage > 100%", func() {

			})
			It("fails creation on negative number", func() {

			})
			It("succeeds creation on percentage between 0%-100%", func() {

			})
		})
	})
	Context("Reconciliation", func() {
		When("few nodes are unhealthy and below max unhealthy", func() {
			It("create a remdiation CR for each unhealthy node", func() {

			})
			It("updates the NHC status with number of healthy nodes", func() {})
			It("updates the NHC status with number of observed nodes", func() {})
		})
		When("few nodes are unhealthy and above max unhealthy", func() {
			It("skips remediation - no CR is created", func() {

			})
			It("deletes a remediation CR for if a node gone healthy", func() {})
			It("updates the NHC status with number of healthy nodes", func() {})
			It("updates the NHC status with number of observed nodes", func() {})
		})
		When("few nodes become healthy", func() {
			It("deletes an existing remediation CR", func() {})
			It("updates the NHC status with number of healthy nodes", func() {})
		})
	})

})
