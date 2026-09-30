package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestKubeletTLSRequiresClusterClientCertificate(t *testing.T) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Minute),
		NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	config := clientcmdapi.NewConfig()
	config.CurrentContext = "kind-test"
	config.Contexts[config.CurrentContext] = &clientcmdapi.Context{Cluster: "test"}
	config.Clusters["test"] = &clientcmdapi.Cluster{CertificateAuthorityData: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})}
	server, err := kubeletTLSConfig(config, "host.docker.internal")
	if err != nil {
		t.Fatal(err)
	}
	if server.ClientAuth != tls.RequireAndVerifyClientCert || len(server.Certificates) != 1 {
		t.Fatalf("unsafe kubelet TLS configuration: %+v", server)
	}
	cert, err := x509.ParseCertificate(server.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := cert.VerifyHostname("host.docker.internal"); err != nil {
		t.Fatal(err)
	}
	if _, err := kubeletTLSConfig(clientcmdapi.NewConfig(), "host.docker.internal"); err == nil {
		t.Fatal("expected missing cluster CA to fail")
	}
}
