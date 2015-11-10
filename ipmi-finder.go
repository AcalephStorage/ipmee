package main

import (
	"log"
	"net"
	"sync"
	"time"
)

func init() {
	log.SetPrefix("IPMIFinder")
}

type IPMIFinder struct {
	Workers          int
	Cidr             string
	RescanInterval   time.Duration
	stopChannel      chan bool
	addServerChannel chan string
	servers          []string
	isActive         bool
	searchWaitGroup  sync.WaitGroup
}

func (i *IPMIFinder) Start() {
	i.stopChannel = make(chan bool)
	i.addServerChannel = make(chan string)
	i.servers = make([]string, 0)
	i.isActive = true
	i.searchWaitGroup = sync.WaitGroup{}
	go i.startProcess()
}

func (i *IPMIFinder) Stop() {
	i.stopChannel <- true
	i.isActive = false
}

func (i *IPMIFinder) ListServers() []string {
	i.searchWaitGroup.Wait()
	return i.servers
}

func (i *IPMIFinder) startProcess() {
	go i.scanServers()
	for i.isActive {
		select {
		case <-time.After(i.RescanInterval):
			go i.scanServers()
		case <-i.stopChannel:
			close(i.stopChannel)
			break
		case server := <-i.addServerChannel:
			i.servers = append(i.servers, server)
		}
	}
}

func (i *IPMIFinder) scanServers() {
	i.servers = nil
	ip, ipnet, err := net.ParseCIDR(i.Cidr)
	if err != nil {
		Error.Println("IPMI Scan - Parse CIDR:", err)
		return
	}
	throttle := make(chan bool, i.Workers)
	for ip := ip.Mask(ipnet.Mask); ipnet.Contains(ip); inc(ip) {
		i.searchWaitGroup.Add(1)
		throttle <- true
		go i.checkIPMI(ip.String(), throttle)
	}
	i.searchWaitGroup.Wait()
}

func (i *IPMIFinder) checkIPMI(ip string, throttle chan bool) {
	defer func() {
		<-throttle
	}()
	defer i.searchWaitGroup.Done()

	ipmiAddr, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(ip, "623"))
	if err != nil {
		Debug.Println("IPMI Scan - Resolve UDP Address:", err)
		return
	}
	conn, err := net.DialUDP("udp4", nil, ipmiAddr)
	if err != nil {
		Debug.Println("IPMI Scan - Dial UDP:", err)
		return
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	ping := []byte{0x06, 0x00, 0x00, 0x06, 0x00, 0x00, 0x11, 0xbe, 0x80, 0x00, 0x00, 0x00}
	_, err = conn.Write(ping)
	if err != nil {
		Debug.Println("IPMI Scan - Ping:", err)
		return
	}
	pong := make([]byte, 28)
	_, _, err = conn.ReadFromUDP(pong)
	if err != nil {
		Debug.Println("IPMI Scan - Awaiting Pong:", err)
		return
	}
	if pong[8] != 0x40 {
		Debug.Println("IPMI Scan - Pong: Server did not Pong.")
		return
	}
	i.addServerChannel <- ip
}

func inc(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}
