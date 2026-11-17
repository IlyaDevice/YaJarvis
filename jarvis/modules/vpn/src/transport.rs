use crate::{
    config::Config,
    protocol::{BoxIo, Target, VlessIo},
};
use rustls::{
    ClientConfig, RootCertStore,
    client::danger::{HandshakeSignatureValid, ServerCertVerified, ServerCertVerifier},
    crypto::CryptoProvider,
    pki_types::{CertificateDer, ServerName, UnixTime},
};
use std::{io, sync::Arc};
use tokio::{io::AsyncWriteExt, net::TcpStream};

pub(crate) fn tls_config(config: &Config) -> Result<Arc<ClientConfig>, String> {
    let provider = Arc::new(rustls::crypto::ring::default_provider());
    let builder = ClientConfig::builder_with_provider(provider.clone())
        .with_safe_default_protocol_versions()
        .map_err(|_| "Не настроить TLS VPN")?;
    let mut tls = if config.insecure {
        builder
            .dangerous()
            .with_custom_certificate_verifier(Arc::new(Insecure(provider)))
            .with_no_client_auth()
    } else {
        builder
            .with_root_certificates(RootCertStore::from_iter(
                webpki_roots::TLS_SERVER_ROOTS.iter().cloned(),
            ))
            .with_no_client_auth()
    };
    if config.websocket {
        tls.alpn_protocols = vec![b"http/1.1".to_vec()];
    }
    Ok(Arc::new(tls))
}

pub(crate) async fn dial(
    config: &Config,
    tls: Arc<ClientConfig>,
    target: &Target,
) -> io::Result<BoxIo> {
    // Resolve only the VPN server locally. The target's domain is encoded into VLESS unchanged.
    let socket = TcpStream::connect((config.host.as_str(), config.port)).await?;
    socket.set_nodelay(true)?;
    if config.reality.is_some() || config.vision {
        let session = crate::reality::client(config).map_err(io::Error::other)?;
        let mut stream = crate::tls_io::TlsIo::new(Box::new(socket), session);
        stream.handshake().await?;
        let mut header = target.vless_header(&config.uuid)?;
        if config.vision {
            // Addons protobuf: field 1 (flow), using the canonical wire flow for both TCP aliases.
            let flow = b"xtls-rprx-vision";
            let mut addon = vec![(flow.len() + 2) as u8, 10, flow.len() as u8];
            addon.extend(flow);
            header.splice(17..18, addon);
        }
        stream.write_all(&header).await?;
        stream.flush().await?;
        return if config.vision {
            Ok(Box::new(crate::vision::VisionIo::new(stream, config.uuid)))
        } else {
            Ok(Box::new(VlessIo::new(Box::new(stream))))
        };
    }
    let mut io: BoxIo = if config.tls {
        let name = ServerName::try_from(config.server_name.clone())
            .map_err(|_| io::Error::other("invalid VPN SNI"))?;
        Box::new(
            tokio_rustls::TlsConnector::from(tls)
                .connect(name, socket)
                .await?,
        )
    } else {
        Box::new(socket)
    };
    if config.websocket {
        io = Box::new(crate::websocket::connect(io, config).await?);
    }
    io.write_all(&target.vless_header(&config.uuid)?).await?;
    io.flush().await?;
    // Do not await the VLESS response here: some servers send it only after the first payload.
    Ok(Box::new(VlessIo::new(io)))
}

#[derive(Debug)]
struct Insecure(Arc<CryptoProvider>);
impl ServerCertVerifier for Insecure {
    fn verify_server_cert(
        &self,
        _: &CertificateDer<'_>,
        _: &[CertificateDer<'_>],
        _: &ServerName<'_>,
        _: &[u8],
        _: UnixTime,
    ) -> Result<ServerCertVerified, rustls::Error> {
        Ok(ServerCertVerified::assertion())
    }
    fn verify_tls12_signature(
        &self,
        msg: &[u8],
        cert: &CertificateDer<'_>,
        signature: &rustls::DigitallySignedStruct,
    ) -> Result<HandshakeSignatureValid, rustls::Error> {
        rustls::crypto::verify_tls12_signature(
            msg,
            cert,
            signature,
            &self.0.signature_verification_algorithms,
        )
    }
    fn verify_tls13_signature(
        &self,
        msg: &[u8],
        cert: &CertificateDer<'_>,
        signature: &rustls::DigitallySignedStruct,
    ) -> Result<HandshakeSignatureValid, rustls::Error> {
        rustls::crypto::verify_tls13_signature(
            msg,
            cert,
            signature,
            &self.0.signature_verification_algorithms,
        )
    }
    fn supported_verify_schemes(&self) -> Vec<rustls::SignatureScheme> {
        self.0.signature_verification_algorithms.supported_schemes()
    }
}
