# Server-Sent Events (SSE) Demo Application

This application demonstrates the use of Server-Sent Events (SSE) using a simple Web-Application developed in Go. The user can start a new deployment by clicking on `Start Deployment` on the home page. The application mocks a deployment by creating a new file for each Deployment request and writing randomly generated sentences to the newly generated file in short intervals. Additionally, the server reads the file continuously and streams any updates in the file to the corresponding clients in real-time over an established SSE connection. The client can view the log updates in a scrollable window.

## Requirements

- Go 1.16+
- Make

# Infrastructure

- Macbook Pro

## Installation

1. Clone the repository:
    ```sh
    git clone https://github.com/yourusername/sse-demo.git
    cd sse-demo
    ```

2. Install dependencies:
    ```sh
    go mod tidy
    ```

## Usage

### Running the Application

To run the application, use the Makefile:

1. **Run the Application**:
    ```sh
    make run
    ```

2. Open your browser and navigate to `http://localhost:8080`.

3. Click the "Start Deployment" button to start generating logs. A new tab will open displaying the log updates in real-time.

### Cleaning Up the Log Directory
The log directory should get cleaned up automatically when the application exits.
To clean up the log directory manually, use the following command:

```sh
make clean