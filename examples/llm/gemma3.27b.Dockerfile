FROM ollama/ollama:latest

ARG MODEL_NAME=gemma3:27b

ENV MODEL_NAME=${MODEL_NAME}

RUN ollama serve & \
    sleep 5 && \
    ollama pull ${MODEL_NAME} && \
    pkill ollama

COPY ask-question.sh /usr/local/bin/ask-question.sh
RUN chmod +x /usr/local/bin/ask-question.sh

ENTRYPOINT ["/usr/local/bin/ask-question.sh"]
