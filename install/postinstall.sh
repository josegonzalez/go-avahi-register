#!/usr/bin/env bash

systemctl --system daemon-reload
systemctl daemon-reload
systemctl enable avahi-register.target
