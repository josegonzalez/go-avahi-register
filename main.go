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
	configFile = flag.String("config", "config.json", "path to the config.json config file")
	ipAddress  = flag.String("ip-address", "", "a hardcoded ip address")
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

func fetchServices() (settings Registry, err error) {
	data, err := ioutil.ReadFile(*configFile)
	if err != nil {
		return settings, err
	}

	err = json.Unmarshal(data, &settings)
	if err != nil {
		return settings, err
	}

	tcpServices := map[string]bool{
		"http":  true,
		"https": true,
	}

	var services []Service
	for _, service := range settings.Services {
		if service.Name == "" {
			return settings, errors.New(`Service "name" field is required`)
		}

		if service.Port == 0 {
			service.Port = 80
		}

		if service.Protocol == "" {
			service.Protocol = "tcp"
		}

		service.ServiceType = fmt.Sprintf("_%v._%v.local.", service.Scheme, service.Protocol)
		if service.Scheme == "" {
			service.ServiceType = fmt.Sprintf("_%v.local.", service.Protocol)
		}

		if tcpServices[service.Scheme] && service.Protocol != "tcp" {
			return settings, errors.New(fmt.Sprintf(`Service "%s" with scheme "%s" must use "tcp" protocol`,
				service.Name, service.Scheme))
		}

		services = append(services, service)
	}

	settings.Services = services
	return settings, nil
}

func publishServices(services []Service, ipAddress string, reverseIPAddress string) error {
	zone, err := mdns.New()
	if err != nil {
		return err
	}

	publishedServices := map[string]bool{}
	publishedServiceTypes := map[string]bool{}
	log.Println("registering services to", ipAddress)
	for _, service := range services {
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

	return nil
}

func main() {
	flag.Parse()

	settings, err := fetchServices()
	if err != nil {
		log.Println("err:", err)
		os.Exit(1)
	}

	ipAddress, err := getIPAddress()
	if err != nil {
		log.Println("err:", err)
		os.Exit(1)
	}

	reverseIPAddress := getReverseIPAddress(ipAddress)
	err = publishServices(settings.Services, ipAddress, reverseIPAddress)
	if err != nil {
		log.Println("err:", err)
		os.Exit(1)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)

	reloadRegistry := func() error {
		settings, err := fetchServices()
		if err != nil {
			return err
		}

		if err = publishServices(settings.Services, ipAddress, reverseIPAddress); err != nil {
			return err
		}
		return nil
	}

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

			default:
				log.Println("received unhandled signal")
				e <- 1
			}
		}
	}()

	code := <-e
	os.Exit(code)
}
