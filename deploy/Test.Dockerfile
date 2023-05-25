FROM scratch

COPY tests /tests

ENTRYPOINT [ "/tests" ]
CMD [ "-test.run", "Integration", "-test.v" ]