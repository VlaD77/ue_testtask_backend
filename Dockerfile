FROM node:22-alpine

WORKDIR /app
COPY package.json ./
COPY src ./src

ENV NODE_ENV=production
ENV HOST=0.0.0.0
ENV PORT=8080
ENV DATA_FILE=/data/progress.json

VOLUME ["/data"]
EXPOSE 8080

CMD ["npm", "start"]

