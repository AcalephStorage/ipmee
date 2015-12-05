package main

import (
	"flag"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"net/http"

	"github.com/emicklei/go-restful"
	"github.com/vmware/goipmi"
)

type Ipmee struct {
	*IPMIFinder
	Username string
	Password string
}

type Server struct {
	Host string `json:"host"`
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

var (
	logLevel     string
	bindHost     string
	bindPort     int
	cidr         string
	ipmiScanners int
	scanInterval int
	username     string
	password     string
)

func main() {

	parseArgs()
	parseEnvs()
	InitLogging(logLevel)
	startServer()
}

func parseArgs() {
	flag.StringVar(&logLevel, "log-level", "INFO", "the log level [ERROR, WARN, INFO, DEBUG].")
	flag.StringVar(&bindHost, "bind-host", "0.0.0.0", "address to bind the api")
	flag.IntVar(&bindPort, "bind-port", 5000, "port to bind the api")
	flag.StringVar(&cidr, "cidr", "", "the CIDR of the network to search for servers.")
	flag.IntVar(&ipmiScanners, "ipmi-scanners", 16, "number of sub-routines to search for servers.")
	flag.IntVar(&scanInterval, "scan-interval", 1800, "interval to rescan for servers. Defaults to 30mins.")
	flag.StringVar(&username, "username", "", "username to access the servers")
	flag.StringVar(&password, "password", "", "password to access the servers")
	flag.Parse()
}

func parseEnvs() {
	if envLogLevel := os.Getenv("IPMEE_LOG_LEVEL"); envLogLevel != "" {
		logLevel = envLogLevel
	}
	if envBindHost := os.Getenv("IPMEE_BIND_HOST"); envBindHost != "" {
		bindHost = envBindHost
	}
	if envBindPort := os.Getenv("IPMEE_BIND_PORT"); envBindPort != "" {
		port, err := strconv.Atoi(envBindPort)
		if err != nil {
			Error.Println("Get Bind Port ENV Var:", err)
			os.Exit(1)
		}
		bindPort = port
	}
	if envCidr := os.Getenv("IPMEE_CIDR"); envCidr != "" {
		cidr = envCidr
	}
	if envIpmiScanners := os.Getenv("IPMEE_IPMI_SCANNERS"); envIpmiScanners != "" {
		scanners, err := strconv.Atoi(envIpmiScanners)
		if err != nil {
			Error.Println("Get IPMI Scanners ENV Var:", err)
			os.Exit(1)
		}
		ipmiScanners = scanners
	}
	if envScanInterval := os.Getenv("IPMEE_SCAN_INTERVAL"); envScanInterval != "" {
		interval, err := strconv.Atoi(envScanInterval)
		if err != nil {
			Error.Println("Get Scan Interval ENV Var:", err)
			os.Exit(1)
		}
		scanInterval = interval
	}
	if envUsername := os.Getenv("IPMEE_USERNAME"); envUsername != "" {
		username = envUsername
	}
	if envPassword := os.Getenv("IPMEE_PASSWORD"); envPassword != "" {
		password = envPassword
	}
}

func startServer() {

	ipmiFinder := &IPMIFinder{
		Workers:        ipmiScanners,
		Cidr:           cidr,
		RescanInterval: time.Duration(scanInterval) * time.Second,
	}

	ipmee := &Ipmee{
		IPMIFinder: ipmiFinder,
		Username:   username,
		Password:   password,
	}
	ipmee.Start()

	container := restful.NewContainer()
	ipmee.register(container)

	address := net.JoinHostPort(bindHost, strconv.Itoa(bindPort))

	server := &http.Server{
		Addr:    address,
		Handler: container,
	}

	Info.Println("Starting IPMEE...")
	server.ListenAndServe()
	// add capture here for signals
	ipmee.Stop()
}

func (ipmee *Ipmee) register(container *restful.Container) {
	ws := &restful.WebService{}

	ws.Path("/api/v1/machines/").
		Produces(restful.MIME_JSON)

	ws.Route(ws.GET("/").To(ipmee.GetMachines).
		Writes([]Server{}))

	ws.Route(ws.GET("/{machine-name}").To(ipmee.GetMachine).
		Param(ws.PathParameter("machine-name", "the name of the machine").DataType("string")).
		Writes(Server{}))

	ws.Route(ws.GET("/{machine-name}/status").To(ipmee.GetMachineStatus).
		Param(ws.PathParameter("machine-name", "the name of the machine").DataType("string")).
		Writes(MachineStatus{}))

	ws.Route(ws.POST("/{machine-name}/{state}").To(ipmee.ChangeMachinePowerState).
		Param(ws.PathParameter("machine-name", "the name of the machine").DataType("string")).
		Param(ws.PathParameter("state", "the power state [on,off,reset,cycle]")))

	container.Add(ws)
}

func (ipmee *Ipmee) GetMachines(req *restful.Request, res *restful.Response) {
	servers := ipmee.ListServers()
	out := make([]Server, len(servers))
	for i, server := range servers {
		out[i] = Server{server}
	}
	res.WriteEntity(out)
}

func (ipmee *Ipmee) GetMachine(req *restful.Request, res *restful.Response) {
	machineName := req.PathParameter("machine-name")
	matchingServer := ipmee.findServer(machineName)
	if matchingServer != nil {
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
		client, err := ipmee.createIpmiClient(matchingServer)
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
		machineStatus.PowerCode = status
		machineStatus.PowerStatus = status.String()
		res.WriteEntity(machineStatus)
	} else {
		res.AddHeader("Content-Type", "text/plain")
		res.WriteErrorString(http.StatusNotFound, "server not found")
	}
}

func (ipmee *Ipmee) ChangeMachinePowerState(req *restful.Request, res *restful.Response) {
	machineName := req.PathParameter("machine-name")
	powerState := req.PathParameter("state")

	var state ipmi.ChassisControl
	switch strings.ToLower(powerState) {
	case "on":
		state = ipmi.ControlPowerUp
	case "off":
		state = ipmi.ControlPowerDown
	case "reset":
		state = ipmi.ControlPowerHardReset
	case "cycle":
		state = ipmi.ControlPowerCycle
	default:
		res.AddHeader("Content-Type", "text/plain")
		res.WriteErrorString(http.StatusNotAcceptable, "power state not supported")
		return
	}

	foundServers := ipmee.ListServers()
	servers := make([]Server, 0)
	if strings.ToLower(machineName) != "all" {
		matchingServer := ipmee.findServer(machineName)
		if matchingServer != nil {
			servers = append(servers, *matchingServer)
		} else {
			res.AddHeader("Content-Type", "text/plain")
			res.WriteErrorString(http.StatusNotFound, "server not found")
		}
	} else {
		for _, srv := range foundServers {
			server := ipmee.findServer(srv)
			servers = append(servers, *server)
		}
	}
	errs := ipmee.changeMachineState(state, servers...)
	if len(errs) > 0 {
		errMessages := make([]string, len(errs))
		for _, err := range errs {
			errMessages = append(errMessages, err.Error())
		}
		errMessage := strings.Join(errMessages, "\n")
		res.AddHeader("Content-Type", "text/plain")
		res.WriteErrorString(http.StatusServiceUnavailable, errMessage)
	}

}

func (ipmee *Ipmee) changeMachineState(state ipmi.ChassisControl, servers ...Server) []error {
	errs := make([]error, 0)
	for _, server := range servers {
		client, err := ipmee.createIpmiClient(&server)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		Info.Printf("Changing %s to %s...", server.Host, state)
		err = client.Control(state)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func (ipmee *Ipmee) createIpmiClient(server *Server) (*ipmi.Client, error) {
	conn := &ipmi.Connection{
		Hostname:  server.Host,
		Port:      623,
		Username:  ipmee.Username,
		Password:  ipmee.Password,
		Interface: "lanplus",
	}
	return ipmi.NewClient(conn)
}

func (ipmee *Ipmee) findServer(serverName string) *Server {
	servers := ipmee.ListServers()
	for _, server := range servers {
		if server == serverName {
			return &Server{server}
		}
	}
	return nil
}
