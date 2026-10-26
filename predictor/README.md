# Prediction Service

- **Runtime**: Python 3.12 service packaged in Docker (host Python 3.14 lacks binary wheels for key ML packages).
- **Model**: Loads a serialized `.pkl` model trained by a teammate to forecast incoming traffic.
- **Contract**: HTTP API request/response schema for metrics ingestion and traffic forecasts will be defined in Part 5.
- **Status**: Scaffold placeholder; implementation pending model delivery.
