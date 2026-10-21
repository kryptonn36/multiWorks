FROM node:22-alpine

WORKDIR /app

COPY --chown=node:node package.json server.mjs index.html app.js api.js endpoints.js storyboard.js stage8.js board-sync.js projectSocket.js styles.css ./

ENV NODE_ENV=production
ENV FRONTEND_HOST=0.0.0.0
ENV FRONTEND_PORT=5173

USER node

EXPOSE 5173

CMD ["npm", "start"]
