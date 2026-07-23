#import <LocalAuthentication/LocalAuthentication.h>

// runPolicy synchronously evaluates one LAPolicy and blocks until the reply fires.
static BOOL runPolicy(LAContext *context, LAPolicy policy, NSString *reason, NSInteger *outErrCode) {
    __block BOOL success = NO;
    __block NSInteger failCode = 0;
    dispatch_semaphore_t sem = dispatch_semaphore_create(0);
    [context evaluatePolicy:policy
             localizedReason:reason
                       reply:^(BOOL ok, NSError *evalErr) {
        success = ok;
        if (evalErr) failCode = evalErr.code;
        dispatch_semaphore_signal(sem);
    }];
    dispatch_semaphore_wait(sem, DISPATCH_TIME_FOREVER);
    *outErrCode = failCode;
    return success;
}

// la_authenticate synchronously requires a FRESH Touch ID scan, falling back to the
// macOS login password only when biometrics are structurally unusable (not
// enrolled, no hardware, or locked out from too many failed attempts). Returns 0 on
// success. On failure returns the LAError code (already negative, e.g. -2
// user-cancelled, -7 not enrolled), or -1000 if evaluation couldn't even start.
//
// This exists because kSecAttrAccessControl's kSecAccessControlUserPresence flag is
// NOT reliably enforced on macOS's legacy (non-Data-Protection) keychain, which is
// what Callisto must use to avoid a keychain-access-groups entitlement that a
// Developer-ID (non-App-Store) build can't hold without amfid refusing to launch
// the binary at all. So Touch ID enforcement happens explicitly here, in code
// Callisto controls, gating every keychain read in secretstore_darwin.go's Get.
//
// LAPolicyDeviceOwnerAuthentication (Touch ID OR passcode) has a macOS-level "the
// user recently authenticated" grace period that can pass silently with no prompt
// at all -- verified empirically, unacceptable for gating a wallet unlock.
// LAPolicyDeviceOwnerAuthenticationWithBiometrics has no such grace period: it
// always requires an actual fresh biometric scan. So that's the primary policy;
// deviceOwnerAuthentication (password) is only used as a fallback when biometrics
// can't be used at all, never as an easier alternate path after a failed/cancelled
// biometric attempt.
int la_authenticate(const char* reason) {
    @autoreleasepool {
        NSString *reasonStr = [NSString stringWithUTF8String:reason];

        LAContext *bioContext = [[LAContext alloc] init];
        NSError *bioAvailErr = nil;
        BOOL bioAvailable = [bioContext canEvaluatePolicy:LAPolicyDeviceOwnerAuthenticationWithBiometrics
                                                     error:&bioAvailErr];
        if (bioAvailable) {
            NSInteger failCode = 0;
            if (runPolicy(bioContext, LAPolicyDeviceOwnerAuthenticationWithBiometrics, reasonStr, &failCode)) {
                return 0;
            }
            // kLAErrorBiometryLockout == -8: too many failed Touch ID attempts.
            // Fall through to the passcode fallback below. Any other failure
            // (user cancel, no match, system cancel, ...) is final -- do not give
            // a second, more lenient attempt via the grace-period-prone policy.
            if (failCode != -8) {
                return (int)failCode;
            }
        }

        // No biometrics enrolled/available, or locked out: fall back to the
        // device passcode / macOS login password.
        LAContext *pwContext = [[LAContext alloc] init];
        NSError *pwAvailErr = nil;
        if (![pwContext canEvaluatePolicy:LAPolicyDeviceOwnerAuthentication error:&pwAvailErr]) {
            return pwAvailErr ? (int)pwAvailErr.code : -1000;
        }
        NSInteger failCode2 = 0;
        if (runPolicy(pwContext, LAPolicyDeviceOwnerAuthentication, reasonStr, &failCode2)) {
            return 0;
        }
        return (int)failCode2;
    }
}
