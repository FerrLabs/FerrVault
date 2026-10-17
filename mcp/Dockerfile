ARG PACKAGE=@ferrvault/mcp

FROM node:24-alpine AS build
ARG PACKAGE
WORKDIR /app
ENV HUSKY=0 \
    CI=true
RUN corepack enable
COPY pnpm-workspace.yaml pnpm-lock.yaml package.json ./
RUN pnpm fetch
COPY packages packages
RUN pnpm install --frozen-lockfile --offline
RUN pnpm run build
RUN pnpm --filter "${PACKAGE}" deploy --prod --legacy /out

FROM node:24-alpine
WORKDIR /app
ENV NODE_ENV=production \
    FERRLABS_MCP_MODE=http \
    PORT=3000 \
    HOST=0.0.0.0
COPY --from=build --chown=1000:1000 /out/node_modules ./node_modules
COPY --from=build --chown=1000:1000 /out/dist ./dist
COPY --from=build --chown=1000:1000 /out/package.json ./
USER 1000:1000
EXPOSE 3000
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://127.0.0.1:3000/health || exit 1
CMD ["node", "dist/index.js"]
