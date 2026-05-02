package com.smartgig.auth.mapper;

import com.smartgig.auth.dto.request.RegisterRequest;
import com.smartgig.auth.entity.UserCredential;
import javax.annotation.processing.Generated;
import org.springframework.stereotype.Component;

@Generated(
    value = "org.mapstruct.ap.MappingProcessor",
    date = "2026-05-02T14:34:41+0700",
    comments = "version: 1.5.5.Final, compiler: Eclipse JDT (IDE) 3.46.0.v20260407-0427, environment: Java 21.0.10 (Eclipse Adoptium)"
)
@Component
public class UserCredentialMapperImpl implements UserCredentialMapper {

    @Override
    public UserCredential toEntity(RegisterRequest request) {
        if ( request == null ) {
            return null;
        }

        UserCredential.UserCredentialBuilder userCredential = UserCredential.builder();

        userCredential.role( request.getRole() );
        userCredential.email( request.getEmail() );
        userCredential.username( request.getUsername() );

        userCredential.isActive( true );
        userCredential.isEmailVerified( false );
        userCredential.failedLoginAttempts( 0 );

        return userCredential.build();
    }
}
