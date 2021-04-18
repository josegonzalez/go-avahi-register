#!/usr/bin/env bash

mkdir -p /etc/avahi-register
if [[ ! -f /etc/avahi-register/config.json ]] && [[ ! -f /etc/avahi-register/config.yml ]] && [[ ! -f /etc/avahi-register/config.yaml ]]; then
  touch /etc/avahi-register/config.yml
  echo "---" > /etc/avahi-register/config.yml
  echo "services: []" >> /etc/avahi-register/config.yml
fi

systemctl --system daemon-reload
systemctl daemon-reload
systemctl enable avahi-register.target
