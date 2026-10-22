FROM nginx:1.27-alpine
COPY web/ /usr/share/nginx/html/
COPY deployments/nginx-frontend.conf /etc/nginx/nginx.conf
EXPOSE 8080
