UPDATE service_pool_reset_monitor
SET config = (config - 'api_format')
|| CASE
    WHEN (config->>'propagation_minutes')::integer IN (15, 30)
        THEN jsonb_build_object('propagation_minutes', 0)
    ELSE '{}'::jsonb
END
WHERE id = 1;
