mod installer;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    installer::run().await
}
