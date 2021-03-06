/*
   Copyright 2020 SUSE

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package mits

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"strconv"

	"github.com/SUSE/minibroker-integration-tests/mits/config"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gexec"

	"github.com/cloudfoundry-incubator/cf-test-helpers/cf"
	"github.com/cloudfoundry-incubator/cf-test-helpers/generator"
	"github.com/cloudfoundry-incubator/cf-test-helpers/workflowhelpers"
)

// SimpleAppAndService asserts that a service can be bound to an app. Apps are expected to perform
// their own assertion on the service. Apps MUST only successfully start after it finished all
// assertions.
func SimpleAppAndService(
	testSetup *workflowhelpers.ReproducibleTestSuiteSetup,
	testConfig config.TestConfig,
	timeouts config.Timeouts,
	serviceBrokerName string,
	appPath string,
	params map[string]interface{},
) {
	orgName := testSetup.TestSpace.OrganizationName()
	spaceName := testSetup.TestSpace.SpaceName()
	appName := generator.PrefixedRandomName(testConfig.Class, "app")
	serviceName := generator.PrefixedRandomName(testConfig.Class, "service")
	securityGroupName := generator.PrefixedRandomName(testConfig.Class, "security-group")

	By("pushing the test app without starting")
	Expect(
		cf.Cf("push", appName, "--no-start", "-p", appPath).
			Wait(timeouts.CFPush),
	).To(Exit(0))
	defer func() {
		cf.Cf("delete", appName, "-r", "-f").Wait(testSetup.ShortTimeout())
	}()
	By("setting the SERVICE_NAME environment variable in the app")
	Expect(
		cf.Cf("set-env", appName, "SERVICE_NAME", serviceName).
			Wait(testSetup.ShortTimeout()),
	).To(Exit(0))

	service := NewService(serviceName, serviceBrokerName, GinkgoWriter, GinkgoWriter)

	By("creating the service instance")
	err := service.Create(testConfig, params, timeouts.CFCreateService)
	Expect(err).NotTo(HaveOccurred())
	defer service.Destroy(testSetup.ShortTimeout())

	By("waiting for the service instance to become ready")
	err = service.WaitForCreate(timeouts.CFCreateService)
	Expect(err).NotTo(HaveOccurred())

	By("binding the service instance to the app")
	err = service.Bind(appName, testSetup.ShortTimeout())
	Expect(err).NotTo(HaveOccurred())
	defer service.Unbind(appName, testSetup.ShortTimeout())

	By("creating and binding a security-group for the service instance")
	credentials, err := service.Credentials(testSetup.ShortTimeout())
	Expect(err).NotTo(HaveOccurred())

	host := credentials["host"].(string)
	port := strconv.Itoa(int(credentials["port"].(float64)))
	hostIP, err := net.LookupIP(host)
	Expect(err).NotTo(HaveOccurred())

	securityGroup := []map[string]string{
		{
			"protocol":    "tcp",
			"destination": fmt.Sprintf("%s/32", hostIP[0]),
			"ports":       port,
			"description": fmt.Sprintf("Allow traffic to %s", serviceName),
		},
	}
	securityGroupFile, err := ioutil.TempFile("", fmt.Sprintf("%s_security_group.json", serviceName))
	Expect(err).NotTo(HaveOccurred())
	defer os.Remove(securityGroupFile.Name())
	encoder := json.NewEncoder(securityGroupFile)
	err = encoder.Encode(securityGroup)
	Expect(err).NotTo(HaveOccurred())
	securityGroupFile.Close()

	workflowhelpers.AsUser(testSetup.AdminUserContext(), testSetup.ShortTimeout(), func() {
		Expect(
			cf.Cf("create-security-group", securityGroupName, securityGroupFile.Name()).
				Wait(testSetup.ShortTimeout()),
		).To(Exit(0))
	})
	defer func() {
		workflowhelpers.AsUser(testSetup.AdminUserContext(), testSetup.ShortTimeout(), func() {
			Expect(
				cf.Cf("delete-security-group", securityGroupName, "-f").
					Wait(testSetup.ShortTimeout()),
			).To(Exit(0))
		})
	}()
	workflowhelpers.AsUser(testSetup.AdminUserContext(), testSetup.ShortTimeout(), func() {
		Expect(
			cf.Cf("bind-security-group", securityGroupName, orgName, spaceName, "--lifecycle", "running").
				Wait(testSetup.ShortTimeout()),
		).To(Exit(0))
	})
	defer func() {
		workflowhelpers.AsUser(testSetup.AdminUserContext(), testSetup.ShortTimeout(), func() {
			Expect(
				cf.Cf("unbind-security-group", securityGroupName, orgName, spaceName, "--lifecycle", "running").
					Wait(testSetup.ShortTimeout()),
			).To(Exit(0))
		})
	}()

	defer func() {
		cf.Cf("logs", appName, "--recent").Wait(testSetup.ShortTimeout())
	}()
	By("starting the app")
	Expect(
		cf.Cf("start", appName).
			Wait(timeouts.CFStart),
	).To(Exit(0))
}
