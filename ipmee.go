package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"

	"encoding/json"
	"io/ioutil"
	"net/http"

	"github.com/emicklei/go-restful"
	"github.com/vmware/goipmi"
)

type Ipmee struct {
	*Config
}

type Config struct {
	ApiHost string   `json:"api_host"`
	ApiPort int      `json:"api_port"`
	Servers []Server `json:"servers"`
}

type Server struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

type MachineStatus struct {
	*Server
	PowerCode   ChassisPowerStatus `json:"power_code"`
	PowerStatus string             `json:"power_status"`
}

type ChassisPowerStatus uint8

func (cps ChassisPowerStatus) String() string {
	switch cps {
	case 32:
		return "OFF"
	case 33:
		return "ON"
	default:
		return "UNKNOWN"
	}
}

func main() {
	var config Config
	loadConfig(&config)
	startServer(&config)
}

func loadConfig(config *Config) {
	configFile := flag.String("config", "config.yaml", "the configuration file")
	flag.Parse()

	// read config file
	// load config from file if exists
	fileData, err := ioutil.ReadFile(*configFile)
	if err != nil {
		fmt.Println("unable to load config file")
		os.Exit(-2)
	}

	if err := json.Unmarshal(fileData, config); err != nil {
		fmt.Println("unable to read config file")
		os.Exit(-3)
	}
}

func startServer(config *Config) {

	ipmee := &Ipmee{config}

	container := restful.NewContainer()
	ipmee.register(container)

	address := ipmee.ApiHost + ":" + strconv.Itoa(ipmee.ApiPort)

	server := &http.Server{
		Addr:    address,
		Handler: container,
	}

	server.ListenAndServe()

	// for _, server := range config.Servers {

	// 	fmt.Printf("Checking status of %s... ", server.Name)

	// 	conn := &ipmi.Connection{
	// 		Hostname:  server.Host,
	// 		Port:      server.Port,
	// 		Username:  server.Username,
	// 		Password:  server.Password,
	// 		Interface: "lanplus",
	// 	}

	// 	client, err := ipmi.NewClient(conn)
	// 	if err != nil {
	// 		fmt.Println("error creating client", err)
	// 		os.Exit(-1)
	// 	}

	// 	ipmee := &Ipmee{client}
	// 	ipmee.setChassisPower(ipmi.ControlPowerUp)
	// 	status, _ := ipmee.chassisPowerStatus()
	// 	fmt.Printf("%s\n", status)
	// }

	// 	conn := &ipmi.Connection{
	// 	Hostname:  "112.198.53.42",
	// 	Port:      5555,
	// 	Username:  "admin",
	// 	Password:  "admin",
	// 	Interface: "lan",
	// }

	// client, err := ipmi.NewClient(conn)
	// if err != nil {
	// 	fmt.Println("error creating client:", err)
	// 	os.Exit(-1) // fail.. but what code?
	// }

	// ipmee := &Ipmee{client}
	// ipmee.chassisStatus()
}

func (ipmee *Ipmee) register(container *restful.Container) {
	ws := &restful.WebService{}

	ws.Path("/api/v1/machines/").
		Consumes(restful.MIME_JSON).
		Produces(restful.MIME_JSON)

	ws.Route(ws.GET("/").To(ipmee.GetMachines).
		Writes([]Server{}))

	ws.Route(ws.GET("/{machine-name}").To(ipmee.GetMachine).
		Param(ws.PathParameter("machine-name", "the name of the machine").DataType("string")).
		Writes(Server{}))

	ws.Route(ws.GET("/{machine-name}/status").To(ipmee.GetMachineStatus).
		Param(ws.PathParameter("machine-name", "the name of the machine").DataType("string")).
		Writes(MachineStatus{}))

	ws.Route(ws.POST("/{machine-name}/on").To(ipmee.PowerOnMachine).
		Param(ws.PathParameter("machine-name", "the name of the machine").DataType("string")))

	ws.Route(ws.POST("/{machine-name}/off").To(ipmee.PowerOffMachine).
		Param(ws.PathParameter("machine-name", "the name of the machine").DataType("string")))

	container.Add(ws)
}

func (ipmee *Ipmee) GetMachines(req *restful.Request, res *restful.Response) {
	result := make([]Server, len(ipmee.Servers))
	copy(result, ipmee.Servers)
	for i := range result {
		result[i].Username = ""
		result[i].Password = ""
	}
	res.WriteEntity(result)
}

func (ipmee *Ipmee) GetMachine(req *restful.Request, res *restful.Response) {
	machineName := req.PathParameter("machine-name")
	matchingServer := ipmee.findServer(machineName)
	if matchingServer != nil {
		matchingServer.Username = ""
		matchingServer.Password = ""
		res.WriteEntity(matchingServer)
	} else {
		res.AddHeader("Content-Type", "text/plain")
		res.WriteErrorString(http.StatusNotFound, "server not found")
	}
}

func (ipmee *Ipmee) GetMachineStatus(req *restful.Request, res *restful.Response) {
	machineName := req.PathParameter("machine-name")
	matchingServer := ipmee.findServer(machineName)
	if matchingServer != nil {
		machineStatus := &MachineStatus{Server: matchingServer}
		ipmiReq := &ipmi.Request{
			NetworkFunction: ipmi.NetworkFunctionChassis,
			Command:         ipmi.CommandChassisStatus,
			Data:            &ipmi.ChassisStatusRequest{},
		}
		ipmiRes := &ipmi.ChassisStatusResponse{}
		client, err := createIpmiClient(matchingServer)
		if err != nil {
			res.AddHeader("Content-Type", "text/plain")
			res.WriteErrorString(http.StatusServiceUnavailable, err.Error())
		}
		err = client.Send(ipmiReq, ipmiRes)
		if err != nil {
			res.AddHeader("Content-Type", "text/plain")
			res.WriteErrorString(http.StatusServiceUnavailable, err.Error())
		}
		status := ChassisPowerStatus(ipmiRes.PowerState)
		machineStatus.Username = ""
		machineStatus.Password = ""
		machineStatus.PowerCode = status
		machineStatus.PowerStatus = status.String()
		res.WriteEntity(machineStatus)
	} else {
		res.AddHeader("Content-Type", "text/plain")
		res.WriteErrorString(http.StatusNotFound, "server not found")
	}
}

func (ipmee *Ipmee) PowerOnMachine(req *restful.Request, res *restful.Response) {
	machineName := req.PathParameter("machine-name")
	matchingServer := ipmee.findServer(machineName)
	if matchingServer != nil {
		err := ipmee.changeMachineState(matchingServer, ipmi.ControlPowerUp)
		if err != nil {
			res.AddHeader("Content-Type", "text/plain")
			res.WriteErrorString(http.StatusServiceUnavailable, err.Error())
		}
	} else {
		res.AddHeader("Content-Type", "text/plain")
		res.WriteErrorString(http.StatusNotFound, "server not found")
	}
}

func (ipmee *Ipmee) PowerOffMachine(req *restful.Request, res *restful.Response) {
	machineName := req.PathParameter("machine-name")
	matchingServer := ipmee.findServer(machineName)
	if matchingServer != nil {
		err := ipmee.changeMachineState(matchingServer, ipmi.ControlPowerDown)
		if err != nil {
			res.AddHeader("Content-Type", "text/plain")
			res.WriteErrorString(http.StatusServiceUnavailable, err.Error())
		}
	} else {
		res.AddHeader("Content-Type", "text/plain")
		res.WriteErrorString(http.StatusNotFound, "server not found")
	}
}

func (ipmee *Ipmee) changeMachineState(server *Server, state ipmi.ChassisControl) error {
	client, err := createIpmiClient(server)
	if err != nil {
		return err
	}
	err = client.Control(state)
	if err != nil {
		return err
	}
	return nil
}

// func (ipmee *Ipmee) setChassisPower(state ipmi.ChassisControl) error {
// 	err := ipmee.client.Control(state)
// 	if err != nil {
// 		fmt.Println("error changing control")
// 	}
// 	return err
// }

func createIpmiClient(server *Server) (*ipmi.Client, error) {
	conn := &ipmi.Connection{
		Hostname:  server.Host,
		Port:      server.Port,
		Username:  server.Username,
		Password:  server.Password,
		Interface: "lanplus",
	}
	return ipmi.NewClient(conn)
}

func (ipmee *Ipmee) findServer(serverName string) *Server {
	for _, server := range ipmee.Servers {
		if server.Name == serverName {
			return &server
		}
	}
	return nil
}
