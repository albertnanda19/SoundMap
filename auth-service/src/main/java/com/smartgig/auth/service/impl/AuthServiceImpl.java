package com.smartgig.auth.service.impl;

import com.smartgig.auth.dto.request.LoginRequest;
import com.smartgig.auth.dto.request.RefreshTokenRequest;
import com.smartgig.auth.dto.request.RegisterRequest;
import com.smartgig.auth.dto.response.AuthResponse;
import com.smartgig.auth.entity.UserCredential;
import com.smartgig.auth.mapper.UserCredentialMapper;
import com.smartgig.auth.repository.UserCredentialRepository;
import com.smartgig.auth.service.AuthService;
import com.smartgig.auth.util.JwtUtil;
import com.smartgig.common.dto.ApiResponse;
import com.smartgig.common.exception.BusinessException;
import com.smartgig.common.exception.UnauthorizedException;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.security.crypto.bcrypt.BCryptPasswordEncoder;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.time.LocalDateTime;
import java.util.Optional;
import java.util.concurrent.TimeUnit;

@Slf4j
@Service
@RequiredArgsConstructor
public class AuthServiceImpl implements AuthService {

    private final UserCredentialRepository userCredentialRepository;
    private final UserCredentialMapper userCredentialMapper;
    private final JwtUtil jwtUtil;
    private final StringRedisTemplate redisTemplate;
    private final BCryptPasswordEncoder passwordEncoder;

    private static final int MAX_FAILED_ATTEMPTS = 5;
    private static final int LOCK_DURATION_MINUTES = 30;

    @Override
    @Transactional
    public ApiResponse<AuthResponse> register(RegisterRequest request) {
        log.info("Registering new user with email: {}", request.getEmail());

        // Validate email uniqueness
        if (userCredentialRepository.existsByEmail(request.getEmail())) {
            throw new BusinessException("Email already registered");
        }

        // Validate username uniqueness
        if (userCredentialRepository.existsByUsername(request.getUsername())) {
            throw new BusinessException("Username already taken");
        }

        // Map to entity
        UserCredential user = userCredentialMapper.toEntity(request);

        // Generate userId (in real app, this might come from user-service)
        Long userId = System.currentTimeMillis();
        user.setUserId(userId);

        // Hash password
        user.setPasswordHash(passwordEncoder.encode(request.getPassword()));

        // Save user
        UserCredential savedUser = userCredentialRepository.save(user);
        log.info("User registered successfully with userId: {}", savedUser.getUserId());

        // Generate tokens
        String accessToken = jwtUtil.generateAccessToken(
                savedUser.getUserId(),
                savedUser.getEmail(),
                savedUser.getRole().name(),
                savedUser.getUsername()
        );
        String refreshToken = jwtUtil.generateRefreshToken(savedUser.getUserId());

        // Store refresh token in Redis (TTL 7 days)
        String refreshKey = "refresh:" + savedUser.getUserId();
        redisTemplate.opsForValue().set(refreshKey, refreshToken, 7, TimeUnit.DAYS);

        AuthResponse authResponse = buildAuthResponse(savedUser, accessToken, refreshToken);

        return ApiResponse.<AuthResponse>builder()
                .success(true)
                .message("Registration successful")
                .data(authResponse)
                .build();
    }

    @Override
    @Transactional
    public ApiResponse<AuthResponse> login(LoginRequest request) {
        log.info("Login attempt for email: {}", request.getEmail());

        // Find user by email
        UserCredential user = userCredentialRepository.findByEmail(request.getEmail())
                .orElseThrow(() -> new UnauthorizedException("Invalid credentials"));

        // Check account lock
        if (user.getLockedUntil() != null && user.getLockedUntil().isAfter(LocalDateTime.now())) {
            throw new BusinessException("Account is locked until " + user.getLockedUntil());
        }

        // Verify password
        if (!passwordEncoder.matches(request.getPassword(), user.getPasswordHash())) {
            // Increment failed attempts
            int failedAttempts = Optional.ofNullable(user.getFailedLoginAttempts()).orElse(0) + 1;
            user.setFailedLoginAttempts(failedAttempts);

            // Lock account if max attempts reached
            if (failedAttempts >= MAX_FAILED_ATTEMPTS) {
                user.setLockedUntil(LocalDateTime.now().plusMinutes(LOCK_DURATION_MINUTES));
                log.warn("Account locked for userId: {} due to {} failed attempts", user.getUserId(), failedAttempts);
            }

            userCredentialRepository.save(user);
            throw new UnauthorizedException("Invalid credentials");
        }

        // Password correct - reset failed attempts and update last login
        user.setFailedLoginAttempts(0);
        user.setLockedUntil(null);
        user.setLastLoginAt(LocalDateTime.now());
        userCredentialRepository.save(user);

        log.info("Login successful for userId: {}", user.getUserId());

        // Generate tokens
        String accessToken = jwtUtil.generateAccessToken(
                user.getUserId(),
                user.getEmail(),
                user.getRole().name(),
                user.getUsername()
        );
        String refreshToken = jwtUtil.generateRefreshToken(user.getUserId());

        // Store refresh token in Redis (TTL 7 days)
        String refreshKey = "refresh:" + user.getUserId();
        redisTemplate.opsForValue().set(refreshKey, refreshToken, 7, TimeUnit.DAYS);

        AuthResponse authResponse = buildAuthResponse(user, accessToken, refreshToken);

        return ApiResponse.<AuthResponse>builder()
                .success(true)
                .message("Login successful")
                .data(authResponse)
                .build();
    }

    @Override
    public ApiResponse<AuthResponse> refreshToken(RefreshTokenRequest request) {
        log.info("Token refresh request received");

        // Validate refresh token
        if (!jwtUtil.validateToken(request.getRefreshToken())) {
            throw new UnauthorizedException("Invalid refresh token");
        }

        // Check if it's a refresh token type
        String tokenType = jwtUtil.extractClaim(request.getRefreshToken(), "type");
        if (!"refresh".equals(tokenType)) {
            throw new UnauthorizedException("Invalid token type");
        }

        // Extract userId
        Long userId = jwtUtil.extractUserId(request.getRefreshToken());

        // Check Redis for token match
        String refreshKey = "refresh:" + userId;
        String storedToken = redisTemplate.opsForValue().get(refreshKey);

        if (storedToken == null || !storedToken.equals(request.getRefreshToken())) {
            throw new UnauthorizedException("Refresh token expired or invalid");
        }

        // Get user details
        UserCredential user = userCredentialRepository.findById(userId)
                .orElseThrow(() -> new UnauthorizedException("User not found"));

        // Generate new access token (keep same refresh token)
        String accessToken = jwtUtil.generateAccessToken(
                user.getUserId(),
                user.getEmail(),
                user.getRole().name(),
                user.getUsername()
        );

        AuthResponse authResponse = buildAuthResponse(user, accessToken, request.getRefreshToken());

        return ApiResponse.<AuthResponse>builder()
                .success(true)
                .message("Token refreshed successfully")
                .data(authResponse)
                .build();
    }

    @Override
    public ApiResponse<Void> logout(Long userId) {
        log.info("Logout request for userId: {}", userId);

        // Delete refresh token from Redis
        String refreshKey = "refresh:" + userId;
        redisTemplate.delete(refreshKey);

        return ApiResponse.<Void>builder()
                .success(true)
                .message("Logout successful")
                .build();
    }

    private AuthResponse buildAuthResponse(UserCredential user, String accessToken, String refreshToken) {
        return AuthResponse.builder()
                .accessToken(accessToken)
                .refreshToken(refreshToken)
                .tokenType("Bearer")
                .expiresIn(86400L) // 24 hours in seconds
                .userId(user.getUserId())
                .username(user.getUsername())
                .email(user.getEmail())
                .role(user.getRole().name())
                .build();
    }
}
