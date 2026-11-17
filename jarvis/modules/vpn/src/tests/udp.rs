use super::*;
use tokio::net::UdpSocket;

pub(super) async fn association(state: &Value) -> (TcpStream, UdpSocket, std::net::SocketAddr) {
    let udp = UdpSocket::bind("127.0.0.1:0").await.unwrap();
    let mut control = TcpStream::connect(state["socks"].as_str().unwrap())
        .await
        .unwrap();
    control.write_all(&[5, 1, 2]).await.unwrap();
    assert_eq!(control.read_u16().await.unwrap(), 0x0502);
    let (user, pass) = (
        state["user"].as_str().unwrap().as_bytes(),
        state["pass"].as_str().unwrap().as_bytes(),
    );
    let mut auth = vec![1, user.len() as u8];
    auth.extend(user);
    auth.push(pass.len() as u8);
    auth.extend(pass);
    control.write_all(&auth).await.unwrap();
    assert_eq!(control.read_u16().await.unwrap(), 0x0100);
    let mut request = vec![5, 3, 0, 1, 127, 0, 0, 1];
    request.extend(udp.local_addr().unwrap().port().to_be_bytes());
    control.write_all(&request).await.unwrap();
    let mut reply = [0; 10];
    control.read_exact(&mut reply).await.unwrap();
    assert_eq!(&reply[..8], &[5, 0, 0, 1, 127, 0, 0, 1]);
    (
        control,
        udp,
        ([127, 0, 0, 1], u16::from_be_bytes([reply[8], reply[9]])).into(),
    )
}
pub(super) fn packet(target: &str, port: u16, payload: &[u8]) -> Vec<u8> {
    let mut out = vec![0, 0, 0, 3, target.len() as u8];
    out.extend(target.as_bytes());
    out.extend(port.to_be_bytes());
    out.extend(payload);
    out
}

#[tokio::test]
async fn udp_preserves_boundaries_remote_dns_and_control_lifetime() {
    tokio::time::timeout(Duration::from_secs(5), async {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let vpn = Vpn::new();
        let state = vpn
            .request(json!({"link":format!("vless://{UUID}@{}",listener.local_addr().unwrap())}))
            .await
            .unwrap();
        assert_eq!(state["udp"], true);
        let server = tokio::spawn(async move {
            let (mut io, _) = listener.accept().await.unwrap();
            let mut prefix = [0; 23];
            io.read_exact(&mut prefix).await.unwrap();
            assert_eq!(&prefix[17..], &[0, 2, 0, 53, 2, 18]);
            let mut domain = [0; 18];
            io.read_exact(&mut domain).await.unwrap();
            assert_eq!(&domain, b"remote-dns.invalid");
            io.write_all(&[0, 2, 9, 8]).await.unwrap();
            for expected in [b"first".as_slice(), b"".as_slice(), b"third".as_slice()] {
                let len = io.read_u16().await.unwrap() as usize;
                let mut data = vec![0; len];
                io.read_exact(&mut data).await.unwrap();
                assert_eq!(data, expected);
                // Deliberately fragment both framing and payload.
                let mut frame = (len as u16).to_be_bytes().to_vec();
                frame.extend(&data);
                for byte in frame {
                    io.write_all(&[byte]).await.unwrap();
                    tokio::task::yield_now().await;
                }
            }
            assert!(io.read_u8().await.is_err());
        });
        let (control, udp, relay) = association(&state).await;
        let other = UdpSocket::bind("127.0.0.1:0").await.unwrap();
        other
            .send_to(&packet("remote-dns.invalid", 53, b"wrong-port"), relay)
            .await
            .unwrap();
        let mut fragmented = packet("remote-dns.invalid", 53, b"fragment");
        fragmented[2] = 1;
        udp.send_to(&fragmented, relay).await.unwrap();
        for payload in [b"first".as_slice(), b"".as_slice(), b"third".as_slice()] {
            let sent = packet("remote-dns.invalid", 53, payload);
            udp.send_to(&sent, relay).await.unwrap();
            let mut response = [0; 1024];
            let (n, from) = udp.recv_from(&mut response).await.unwrap();
            assert_eq!(from, relay);
            assert_eq!(&response[..n], &sent);
        }
        drop(control);
        server.await.unwrap();
        udp.send_to(&packet("remote-dns.invalid", 53, b"closed"), relay)
            .await
            .unwrap();
        assert!(!matches!(
            tokio::time::timeout(Duration::from_millis(75), udp.recv(&mut [0; 32])).await,
            Ok(Ok(_))
        ));
        vpn.stop().await;
    })
    .await
    .unwrap();
}

#[tokio::test]
async fn udp_replacement_closes_association_and_vision_does_not_advertise_udp() {
    tokio::time::timeout(Duration::from_secs(4),async{
        let vpn=Vpn::new();let state=vpn.request(json!({"link":format!("vless://{UUID}@127.0.0.1:9")})).await.unwrap();
        let (mut control,_,_)=association(&state).await;
        vpn.request(json!({"link":""})).await.unwrap();assert!(control.read_u8().await.is_err());
        let state=vpn.request(json!({"link":format!("vless://{UUID}@127.0.0.1:9?security=tls&flow=xtls-rprx-vision")})).await.unwrap();
        assert_eq!(state["udp"],false);vpn.stop().await;
    }).await.unwrap();
}

#[test]
fn malformed_udp_packets_never_panic_or_resolve_locally() {
    let full = packet("remote.invalid", 443, b"test");
    for end in 0..full.len() - 4 {
        assert!(crate::udp::decode(&full[..end]).is_none());
    }
    for index in [0, 1, 2, 3] {
        let mut invalid = full.clone();
        invalid[index] = 255;
        assert!(crate::udp::decode(&invalid).is_none());
    }
    let (target, payload) = crate::udp::decode(&full).unwrap();
    assert_eq!(target.port, 443);
    assert_eq!(payload, b"test");
}
