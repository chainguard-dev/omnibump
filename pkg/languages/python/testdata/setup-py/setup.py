from setuptools import setup, find_packages

setup(
    name="myapp",
    version="1.0.0",
    description="Sample application",
    packages=find_packages(),
    install_requires=[
        "Django>=4.2,<5.0",
        "celery>=5.3.0",
        "redis>=4.5.0",
        "psycopg2-binary==2.9.6",
        "requests>=2.28.0",
    ],
)
