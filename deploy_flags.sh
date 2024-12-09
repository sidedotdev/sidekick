#!/bin/bash
set -e

FLAGS_FILE=flags.yml
S3_BUCKET=genflow.dev
OBJECT_PATH=sidekick/$FLAGS_FILE
AWS_CLI=$(which aws)

# Check if AWS CLI is installed
if [ -z "$AWS_CLI" ]; then
    echo "Error: AWS CLI is not installed. Please install it and try again."
    exit 1
fi

# Upload the file to the specified S3 bucket
echo "Uploading $FLAGS_FILE to s3://$S3_BUCKET/..."
aws s3 cp $FLAGS_FILE s3://$S3_BUCKET/$OBJECT_PATH --metadata-directive REPLACE --cache-control max-age=30

# Check if the command was successful
if [ $? -eq 0 ]; then
    echo "Upload successful."
else
    echo "Error: Upload failed."
    exit 1
fi