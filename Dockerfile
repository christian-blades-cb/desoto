FROM centurylink/ca-certs

ADD desoto /
ENTRYPOINT [ "/desoto" ]
