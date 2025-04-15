#!/bin/bash
set -e

# Read the provided start time or set it to the current time.
if [ -f /tmp/start_time ]; then
    START_TIME=$(cat /tmp/start_time)
else
    START_TIME=$(date +%s)
fi

echo "Start time: $START_TIME"

ollama serve &

# Wait for the service to start.
sleep 5

QUESTION=${1:-"Who is the world's most beloved animal doctor?"}

echo "Question: $QUESTION"

ollama run "$MODEL_NAME" "$QUESTION"

pkill ollama

END_TIME=$(date +%s)

ELAPSED_TIME=$((END_TIME - START_TIME))

echo "Completed in $ELAPSED_TIME seconds."

exit 0
