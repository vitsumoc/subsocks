package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io/ioutil"
	"log"
	"net"
	"os"
	"time"

	"github.com/luyuhuang/subsocks/client"
	"github.com/luyuhuang/subsocks/utils"
	"github.com/pelletier/go-toml"
	"golang.org/x/crypto/ssh"
)

func launchClient(t *toml.Tree) {
	config := struct {
		Listen   string `toml:"listen" default:"127.0.0.1:1080"`
		Username string `toml:"username"`
		Password string `toml:"password"`
		Server   struct {
			Protocol string `toml:"protocol"`
			Addr     string `toml:"address"`
		} `toml:"server"`
		HTTP struct {
			Path string `toml:"path" default:"/"`
		} `toml:"http"`
		WS struct {
			Path string `toml:"path" default:"/"`
		} `toml:"ws"`
		TLS struct {
			SkipVerify bool   `toml:"skip_verify"`
			CA         string `toml:"ca"`
		} `toml:"tls"`
		SSH struct {
			Key        string `toml:"key"`
			Passphrase string `toml:"passphrase"`
		} `toml:"ssh"`
	}{}

	if err := t.Unmarshal(&config); err != nil {
		log.Fatalf("Parse '[client]' configuration failed: %s", err)
	}

	cli := client.NewClient(config.Listen)
	cli.Config.Username = config.Username
	cli.Config.Password = config.Password
	cli.Config.ServerProtocol = config.Server.Protocol
	cli.Config.ServerAddr = config.Server.Addr
	cli.Config.HTTPPath = config.HTTP.Path
	cli.Config.WSPath = config.WS.Path

	switch users := t.Get("users").(type) {
	case string:
		cli.Config.Verify = utils.VerifyByHtpasswd(users)
	case *toml.Tree:
		m := make(map[string]string)
		if err := users.Unmarshal(&m); err != nil {
			log.Fatalf("Parse 'server.users' configuration failed: %s", err)
		}
		cli.Config.Verify = utils.VerifyByMap(m)
	}

	switch rules := t.Get("rules").(type) {
	case string:
		r, err := client.NewRulesFromFile(rules)
		if err != nil {
			log.Fatalf("Load rule file failed: %s", err)
		}
		cli.Rules = r
	case *toml.Tree:
		m := make(map[string]string)
		if err := rules.Unmarshal(&m); err != nil {
			log.Fatalf("Parse 'client.rules' configuration failed: %s", err)
		}
		r, err := client.NewRulesFromMap(m)
		if err != nil {
			log.Fatalf("Load rules file failed: %s", err)
		}
		cli.Rules = r
	}

	if needsTLS[config.Server.Protocol] {
		tlsConfig, err := getClientTLSConfig(config.Server.Addr, config.TLS.CA, config.TLS.SkipVerify)
		if err != nil {
			log.Fatalf("Get TLS configuration failed: %s", err)
		}
		cli.TLSConfig = tlsConfig
	}

	if config.Server.Protocol == "ssh" {
		sshConfig, err := getClientSSHConfit(config.Username, config.Password, config.SSH.Key, config.SSH.Passphrase)
		if err != nil {
			log.Fatalf("Get SSH configuration failed: %s", err)
		}
		cli.SSHConfig = sshConfig
	}

	if err := cli.Serve(); err != nil {
		log.Fatalf("Launch client failed: %s", err)
	}
}

func getClientTLSConfig(addr, ca string, skipVerify bool) (config *tls.Config, err error) {
	rootCAs, err := loadCA(ca)
	if err != nil {
		return
	}
	serverName, _, _ := net.SplitHostPort(addr)
	if net.ParseIP(serverName) != nil { // server name is IP
		config = &tls.Config{
			InsecureSkipVerify: true,
			VerifyConnection: func(cs tls.ConnectionState) error { // verify manually
				if skipVerify {
					return nil
				}

				opts := x509.VerifyOptions{
					Roots:         rootCAs,
					CurrentTime:   time.Now(),
					Intermediates: x509.NewCertPool(),
				}

				certs := cs.PeerCertificates
				for i, cert := range certs {
					if i == 0 {
						continue
					}
					opts.Intermediates.AddCert(cert)
				}

				_, err := certs[0].Verify(opts)
				return err
			},
		}
	} else { // server name is domain
		config = &tls.Config{
			ServerName:         serverName,
			RootCAs:            rootCAs,
			InsecureSkipVerify: skipVerify,
		}
	}

	return
}

func loadCA(caFile string) (cp *x509.CertPool, err error) {
	if caFile == "" {
		return
	}
	cp = x509.NewCertPool()
	data, err := ioutil.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	if !cp.AppendCertsFromPEM(data) {
		return nil, errors.New("AppendCertsFromPEM failed")
	}
	return
}

func getClientSSHConfit(username, password, key, passphrase string) (*ssh.ClientConfig, error) {
	sshConf := &ssh.ClientConfig{
		Auth:            []ssh.AuthMethod{},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	// auth by user and pass
	if username != "" && password != "" {
		sshConf.User = username
		sshConf.Auth = append(sshConf.Auth, ssh.Password(password))
	}

	// auth by key pair
	if key != "" {
		pemBytes, err := os.ReadFile(key)
		if err != nil {
			return nil, err
		}
		var signer ssh.Signer
		// privateKey with passphrase
		if passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(pemBytes, []byte(passphrase))
			if err != nil {
				return nil, err
			}
		} else {
			signer, err = ssh.ParsePrivateKey(pemBytes)
			if err != nil {
				return nil, err
			}
		}
		sshConf.Auth = append(sshConf.Auth, ssh.PublicKeys(signer))
	}

	return sshConf, nil
}
