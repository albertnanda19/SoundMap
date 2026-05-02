package com.smartgig.gateway.config;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.smartgig.common.dto.ApiResponse;
import lombok.extern.slf4j.Slf4j;
import org.springframework.boot.web.reactive.error.ErrorWebExceptionHandler;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.core.annotation.Order;
import org.springframework.core.io.buffer.DataBuffer;
import org.springframework.http.HttpStatus;
import org.springframework.http.MediaType;
import org.springframework.web.server.ResponseStatusException;
import org.springframework.web.server.ServerWebExchange;
import reactor.core.publisher.Mono;

import java.nio.charset.StandardCharsets;

@Slf4j
@Configuration
public class GatewayConfig {

    @Bean
    @Order(-1)
    public ErrorWebExceptionHandler gatewayErrorHandler(ObjectMapper objectMapper) {
        return new GatewayErrorWebExceptionHandler(objectMapper);
    }

    public static class GatewayErrorWebExceptionHandler implements ErrorWebExceptionHandler {

        private final ObjectMapper objectMapper;

        public GatewayErrorWebExceptionHandler(ObjectMapper objectMapper) {
            this.objectMapper = objectMapper;
        }

        @Override
        public Mono<Void> handle(ServerWebExchange exchange, Throwable ex) {
            ServerWebExchange exchangeToUse = exchange;
            
            if (exchange.getResponse().isCommitted()) {
                return Mono.error(ex);
            }

            HttpStatus status = determineHttpStatus(ex);
            String message = determineMessage(ex, status);

            exchange.getResponse().setStatusCode(status);
            exchange.getResponse().getHeaders().setContentType(MediaType.APPLICATION_JSON);

            ApiResponse<Void> apiResponse = ApiResponse.<Void>builder()
                    .success(false)
                    .message(message)
                    .build();

            try {
                String json = objectMapper.writeValueAsString(apiResponse);
                DataBuffer buffer = exchange.getResponse().bufferFactory().wrap(json.getBytes(StandardCharsets.UTF_8));
                return exchange.getResponse().writeWith(Mono.just(buffer));
            } catch (JsonProcessingException e) {
                log.error("Error writing error response", e);
                return exchange.getResponse().setComplete();
            }
        }

        private HttpStatus determineHttpStatus(Throwable ex) {
            if (ex instanceof ResponseStatusException) {
                return (HttpStatus) ((ResponseStatusException) ex).getStatusCode();
            }
            if (ex instanceof org.springframework.cloud.gateway.support.NotFoundException) {
                return HttpStatus.NOT_FOUND;
            }
            if (ex instanceof io.github.resilience4j.ratelimiter.RequestNotPermitted) {
                return HttpStatus.TOO_MANY_REQUESTS;
            }
            return HttpStatus.INTERNAL_SERVER_ERROR;
        }

        private String determineMessage(Throwable ex, HttpStatus status) {
            if (ex instanceof io.github.resilience4j.ratelimiter.RequestNotPermitted) {
                return "Rate limit exceeded. Please try again later.";
            }
            if (ex.getMessage() != null && !ex.getMessage().isEmpty()) {
                return ex.getMessage();
            }
            return status.getReasonPhrase();
        }
    }
}
