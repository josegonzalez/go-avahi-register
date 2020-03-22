package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/josegonzalez/mdns"
)

type Service struct {
	Name     string `json:"name"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Scheme   string `json:"scheme"`
	Type     string
}

type Registry struct {
	Services []Service `json:"services"`
}

var (
	configFile        = flag.String("config", "config.json", "path to the config.json config file")
	ipAddress         = flag.String("ip-address", "", "a hardcoded ip address")
	registry          *Registry
	registryLock      = new(sync.RWMutex)
	zone              *mdns.Zone
	zoneLock          = new(sync.RWMutex)
	publishedServices = map[string]Service{}
	publishedTypes    = map[string]bool{}
)

func (s *Service) String() string {
	return fmt.Sprintf("%v+%v://%v.local:%v", s.Scheme, s.Protocol, s.Name, s.Port)
}

func getIPAddress() (string, error) {
	if *ipAddress != "" {
		return *ipAddress, nil
	}

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}

	for _, address := range addrs {
		// check the address type and if it is not a loopback the display it
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String(), nil
			}
		}
	}
	return "", errors.New("Could not retrieve ip address")
}

func getReverseIPAddress(ipAddress string) string {
	pieces := strings.Split(ipAddress, ".")
	for i := len(pieces)/2 - 1; i >= 0; i-- {
		opp := len(pieces) - 1 - i
		pieces[i], pieces[opp] = pieces[opp], pieces[i]
	}

	return strings.Join(pieces, ".")
}

func loadRegistry() (err error) {
	data, err := ioutil.ReadFile(*configFile)
	if err != nil {
		return err
	}

	temp := new(Registry)
	err = json.Unmarshal(data, temp)
	if err != nil {
		return err
	}

	var services []Service
	for _, service := range temp.Services {
		s, err := hydrateService(service)
		if err != nil {
			return err
		}

		services = append(services, s)
	}

	temp.Services = services

	registryLock.Lock()
	registry = temp
	registryLock.Unlock()

	return nil
}

func hydrateService(s Service) (Service, error) {
	if s.Name == "" {
		return s, errors.New(`Service "name" field is required`)
	}

	if s.Port == 0 {
		s.Port = 80
	}

	if s.Protocol == "" {
		s.Protocol = "tcp"
	}

	s.Type = fmt.Sprintf("_%v._%v.local.", s.Scheme, s.Protocol)
	if s.Scheme == "" {
		s.Type = fmt.Sprintf("_%v.local.", s.Protocol)
	}

	tcpServices := map[string]bool{
		"http":  true,
		"https": true,
	}

	if tcpServices[s.Scheme] && s.Protocol != "tcp" {
		return s, errors.New(fmt.Sprintf(`Service "%s" with scheme "%s" must use "tcp" protocol`,
			s.Name, s.Scheme))
	}

	return s, nil
}

func getRegistry() *Registry {
	registryLock.RLock()
	defer registryLock.RUnlock()
	return registry
}

func publishServices(ipAddress string, reverseIPAddress string) error {
	r := getRegistry()

	zoneLock.Lock()
	seenServices := map[string]bool{}
	seenTypes := map[string]bool{}

	for _, service := range r.Services {
		name := service.Name
		serviceType := service.Type
		serviceKey := fmt.Sprintf("%v %v", serviceType, name)

		seenServices[serviceKey] = true
		seenTypes[serviceType] = true

		if _, ok := publishedServices[serviceKey]; !ok {
			log.Println("publishing", service.String())
			publishService(service, ipAddress, reverseIPAddress)
			publishedServices[serviceKey] = service
		}

		if !publishedTypes[serviceType] {
			log.Println("registering type", serviceType)
			zone.Publish(fmt.Sprintf("_services._dns-sd._udp.local. 60 IN PTR %v", serviceType))
			publishedTypes[serviceType] = true
		}

		zone.Publish(fmt.Sprintf("%v.%v 60 IN SRV 0 0 %v %[1]v.local.", name, serviceType, service.Port))
	}

	for serviceKey, service := range publishedServices {
		if !seenServices[serviceKey] {
			log.Println("unpublishing", service.String())
			unpublishService(service, ipAddress, reverseIPAddress)
			delete(publishedServices, serviceKey)
		}
	}

	for serviceType, _ := range publishedTypes {
		if !seenTypes[serviceType] {
			log.Println("deregistering type", serviceType)
			zone.Unpublish(fmt.Sprintf("_services._dns-sd._udp.local. 60 IN PTR %v", serviceType))
			delete(publishedTypes, serviceType)
		}
	}

	zoneLock.Unlock()
	return nil
}

func publishService(service Service, ipAddress string, reverseIPAddress string) {
	zone.Publish(fmt.Sprintf("%v.local. 60 IN A %v", service.Name, ipAddress))
	zone.Publish(fmt.Sprintf("%v.in-addr.arpa. 60 IN PTR %s.local.", reverseIPAddress, service.Name))
	zone.Publish(fmt.Sprintf("%v 60 IN PTR %v.%[1]v", service.Type, service.Name))
	zone.Publish(fmt.Sprintf(`%v.%v 60 IN TXT ""`, service.Name, service.Type))
}

func unpublishService(service Service, ipAddress string, reverseIPAddress string) {
	zone.Unpublish(fmt.Sprintf("%v.local. 60 IN A %v", service.Name, ipAddress))
	zone.Unpublish(fmt.Sprintf("%v.in-addr.arpa. 60 IN PTR %s.local.", reverseIPAddress, service.Name))
	zone.Unpublish(fmt.Sprintf("%v 60 IN PTR %v.%[1]v", service.Type, service.Name))
	zone.Unpublish(fmt.Sprintf(`%v.%v 60 IN TXT ""`, service.Name, service.Type))
	zone.Unpublish(fmt.Sprintf("%v.%v 60 IN SRV 0 0 %v %[1]v.local.", service.Name, service.Type, service.Port))
}

func main() {
	flag.Parse()

	z, err := mdns.New()
	if err != nil {
		log.Println("err:", err)
		os.Exit(1)
	}

	zone = z

	ipAddress, err := getIPAddress()
	if err != nil {
		log.Println("err:", err)
		os.Exit(1)
	}

	reverseIPAddress := getReverseIPAddress(ipAddress)

	reloadServices := func() error {
		if err := loadRegistry(); err != nil {
			return err
		}

		if err := publishServices(ipAddress, reverseIPAddress); err != nil {
			return err
		}
		return nil
	}

	log.Println("registering services to", ipAddress)
	if err = reloadServices(); err != nil {
		log.Println("err:", err)
		os.Exit(1)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGQUIT,
		syscall.SIGTERM,
		syscall.SIGUSR2)

	e := make(chan int)
	go func() {
		for {
			s := <-c
			switch s {
			// kill -SIGHUP XXXX
			case syscall.SIGHUP:
				log.Println("received SIGHUP")
				if err := reloadServices(); err != nil {
					log.Println("err:", err)
					e <- 1
				}

			// kill -SIGUSR2 XXXX
			case syscall.SIGUSR2:
				log.Println("received SIGUSR2")
				if err := reloadServices(); err != nil {
					log.Println("err:", err)
					e <- 1
				}

			case syscall.SIGINT:
				log.Println("received SIGINT")
				e <- 0

			case syscall.SIGQUIT:
				log.Println("received SIGQUIT")
				e <- 0

			case syscall.SIGTERM:
				log.Println("received SIGTERM")
				e <- 0

			default:
				log.Println("received unhandled signal")
				e <- 1
			}
		}
	}()

	code := <-e

	log.Println("exiting with code", code)
	os.Exit(code)
}
