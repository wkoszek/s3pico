#!/bin/bash

# Start the s3mini server
echo "Starting s3mini server on port 8081..."
./s3mini server --data ./test-data --port 8081 &
SERVER_PID=$!
sleep 2

# Create test files
echo "Creating test files..."
mkdir -p test-upload
echo "Hello from file1" > test-upload/file1.txt
echo "Hello from file2" > test-upload/file2.txt
echo "Hello from file3" > test-upload/file3.txt

# Upload files using s3mini
echo "Uploading files with s3mini..."
./s3mini put --port 8081 test-upload/file1.txt testbucket/file1.txt
./s3mini put --port 8081 test-upload/file2.txt testbucket/file2.txt
./s3mini put --port 8081 test-upload/file3.txt testbucket/subfolder/file3.txt

# Test with AWS CLI
echo ""
echo "Testing with AWS CLI..."
echo "========================"

# Configure AWS CLI to use our local server
export AWS_ACCESS_KEY_ID=dummy
export AWS_SECRET_ACCESS_KEY=dummy
export AWS_DEFAULT_REGION=us-east-1

# Create bucket with s3mini client
echo "Creating bucket with s3mini client:"
./s3mini mb --port 8081 newbucket

# Create bucket with AWS CLI
echo ""
echo "Creating another bucket with AWS CLI:"
aws --endpoint-url http://localhost:8081 s3 mb s3://awsbucket --no-sign-request

echo ""
echo "Verifying buckets were created by listing them:"
aws --endpoint-url http://localhost:8081 s3 ls s3://newbucket/ --no-sign-request
aws --endpoint-url http://localhost:8081 s3 ls s3://awsbucket/ --no-sign-request

echo ""
echo "Testing upload to new bucket:"
./s3mini put --port 8081 test-upload/file1.txt newbucket/uploaded-file.txt
aws --endpoint-url http://localhost:8081 s3 ls s3://newbucket/ --no-sign-request

# List all files in the bucket
echo "Listing all files:"
aws --endpoint-url http://localhost:8081 s3 ls s3://testbucket/ --recursive --no-sign-request

# List files in root only
echo ""
echo "Listing root files only:"
aws --endpoint-url http://localhost:8081 s3 ls s3://testbucket/ --no-sign-request

# Get a file
echo ""
echo "Downloading file with AWS CLI:"
aws --endpoint-url http://localhost:8081 s3 cp s3://testbucket/file1.txt ./downloaded-file1.txt --no-sign-request
cat downloaded-file1.txt

# Upload a file with AWS CLI
echo ""
echo "Uploading file with AWS CLI:"
echo "AWS CLI upload test" > aws-upload-test.txt
aws --endpoint-url http://localhost:8081 s3 cp aws-upload-test.txt s3://testbucket/aws-test.txt --no-sign-request

# List again to see the new file
echo ""
echo "Listing after AWS CLI upload:"
aws --endpoint-url http://localhost:8081 s3 ls s3://testbucket/ --recursive --no-sign-request

# Cleanup
echo ""
echo "Cleaning up..."
kill $SERVER_PID
rm -rf test-data test-upload downloaded-file1.txt aws-upload-test.txt

echo "Test complete!"