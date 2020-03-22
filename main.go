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

	"github.com/eliasgs/mdns"
)

type Service struct {
	Name        string `json:"name"`
	Port        int    `json:"port"`
	Protocol    string `json:"protocol"`
	Scheme      string `json:"scheme"`
	ServiceType string
}

type Registry struct {
	Services []Service `json:"services"`
}

var (
	configFile   = flag.String("config", "config.json", "path to the config.json config file")
	ipAddress    = flag.String("ip-address", "", "a hardcoded ip address")
	registry     *Registry
	registryLock = new(sync.RWMutex)
	zone         *mdns.Zone
	zoneLock     = new(sync.RWMutex)
)

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

	s.ServiceType = fmt.Sprintf("_%v._%v.local.", s.Scheme, s.Protocol)
	if s.Scheme == "" {
		s.ServiceType = fmt.Sprintf("_%v.local.", s.Protocol)
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

	publishedServices := map[string]bool{}
	publishedServiceTypes := map[string]bool{}
	zoneLock.Lock()
	log.Println("registering services to", ipAddress)
	for _, service := range r.Services {
		name := service.Name
		serviceType := service.ServiceType
		serviceKey := fmt.Sprintf("%v %v", serviceType, name)

		log.Println("registering", serviceType, service.Port, name)
		if !publishedServices[serviceKey] {
			zone.Publish(fmt.Sprintf("%v.local. 60 IN A %v", name, ipAddress))
			zone.Publish(fmt.Sprintf("%v.in-addr.arpa. 60 IN PTR %s.local.", reverseIPAddress, name))
			zone.Publish(fmt.Sprintf("%v 60 IN PTR %v.%[1]v", serviceType, name))
			zone.Publish(fmt.Sprintf(`%v.%v 60 IN TXT ""`, name, serviceType))
			publishedServices[serviceKey] = true
		}

		if !publishedServiceTypes[serviceType] {
			zone.Publish(fmt.Sprintf("_services._dns-sd._udp.local. 60 IN PTR %v", serviceType))
			publishedServiceTypes[serviceType] = true
		}

		zone.Publish(fmt.Sprintf("%v.%v 60 IN SRV 0 0 %v %[1]v.local.", name, serviceType, service.Port))
	}

	zoneLock.Unlock()
	return nil
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

	reloadRegistry := func() error {
		if err := loadRegistry(); err != nil {
			return err
		}

		if err := publishServices(ipAddress, reverseIPAddress); err != nil {
			return err
		}
		return nil
	}

	if err = reloadRegistry(); err != nil {
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
				log.Println("Received SIGHUP")
				if err := reloadRegistry(); err != nil {
					log.Println("err:", err)
					e <- 1
				}

			// kill -SIGUSR2 XXXX
			case syscall.SIGUSR2:
				log.Println("Received SIGUSR2")
				if err := reloadRegistry(); err != nil {
					log.Println("err:", err)
					e <- 1
				}

			default:
				log.Println("received unhandled signal")
				e <- 1
			}
		}
	}()

	code := <-e
	os.Exit(code)
}
