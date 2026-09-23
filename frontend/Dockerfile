FROM node:22-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY . .
RUN npm run build

# Production: official nginx (its root master process can read Let's Encrypt keys).
# TLS and routing config are mounted by docker-compose.prod.yml from deploy/nginx/.
FROM nginx:1.30-alpine AS prod
COPY --from=build /app/dist /usr/share/nginx/html

# Local development (default target): unprivileged nginx on :8080, see nginx.conf.
FROM nginxinc/nginx-unprivileged:1.30 AS local
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 8080
