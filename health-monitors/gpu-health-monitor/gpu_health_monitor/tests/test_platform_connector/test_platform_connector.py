# Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

from gpu_health_monitor.dcgm_watcher import dcgm
from threading import Event
import grpc
import time
import unittest
from typing import Any
from concurrent import futures
from gpu_health_monitor.dcgm_watcher import types as dcgmtypes
from gpu_health_monitor.platform_connector import platform_connector

from gpu_health_monitor.protos import (
    health_event_pb2 as platformconnector_pb2,
    health_event_pb2_grpc as platformconnector_pb2_grpc,
)
from google.protobuf.timestamp_pb2 import Timestamp

socket_path = "/tmp/nvsentinel.sock"
node_name = "node1"


class PlatformConnectorServicer(platformconnector_pb2_grpc.PlatformConnectorServicer):
    def __init__(self) -> None:
        self.health_events: platformconnector_pb2.HealthEvents = None
        self.health_event: platformconnector_pb2.HealthEvent = None

    def HealthEventOccurredV1(self, request: platformconnector_pb2.HealthEvents, context: Any):
        assert isinstance(request, platformconnector_pb2.HealthEvents) == True
        self.health_events = request.events
        return platformconnector_pb2.HealthEvents()


class TestPlatformConnectors(unittest.TestCase):

    def test_health_event_occurred(self):
        healthEventProcessor = PlatformConnectorServicer()
        server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))
        platformconnector_pb2_grpc.add_PlatformConnectorServicer_to_server(healthEventProcessor, server)
        server.add_insecure_port(f"unix://{socket_path}")
        server.start()
        watcher = dcgm.DCGMWatcher(
            addr="localhost:5555",
            poll_interval_seconds=10,
            callbacks=[],
            dcgm_k8s_service_enabled=False,
        )
        gpu_serials = {
            0: "1650924060039",
            1: "1650924060040",
            2: "1650924060041",
            3: "1650924060042",
            4: "1650924060043",
            5: "1650924060044",
            6: "1650924060045",
            7: "1650924060046",
        }
        exit = Event()

        dcgm_errors_info_dict = {}
        dcgm_errors_info_dict["DCGM_FR_UNKNOWN"] = "CONTACT_SUPPORT"
        dcgm_errors_info_dict["DCGM_FR_UNRECOGNIZED"] = "CONTACT_SUPPORT"
        dcgm_errors_info_dict["DCGM_FR_PCI_REPLAY_RATE"] = "CONTACT_SUPPORT"
        dcgm_errors_info_dict["DCGM_FR_VOLATILE_DBE_DETECTED"] = "COMPONENT_RESET"
        dcgm_errors_info_dict["DCGM_FR_VOLATILE_SBE_DETECTED"] = "IGNORE"
        dcgm_errors_info_dict["DCGM_FR_PENDING_PAGE_RETIREMENTS"] = "IGNORE"
        dcgm_errors_info_dict["DCGM_FR_RETIRED_PAGES_LIMIT"] = "CONTECT_SUPPORT"
        dcgm_errors_info_dict["DCGM_FR_CORRUPT_INFOROM"] = "COMPONENT_RESET"
        dcgm_errors_info_dict["DCGM_HEALTH_WATCH_INFOROM"] = "IGNORE"

        dcgm_health_conditions_categorization_mapping_config = {}
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_THERMAL"] = "NonFatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_POWER"] = "NonFatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_NVLINK"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_SM"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_MEM"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_INFOROM"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_MCU"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_DRIVER"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_NVSWITCH_FATAL"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_NVSWITCH_NONFATAL"] = "NonFatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_PCIE"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_PMU"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_CPUSET"] = "NonFatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_NVSWITCH"] = "Fatal"

        platform_connector_test = platform_connector.PlatformConnectorEventProcessor(
            socket_path,
            node_name,
            exit,
            dcgm_errors_info_dict,
            "statefile",
            dcgm_health_conditions_categorization_mapping_config,
        )
        dcgm_health_events = watcher._get_health_status_dict()
        dcgm_health_events["DCGM_HEALTH_WATCH_INFOROM"] = dcgmtypes.HealthDetails(
            status=dcgmtypes.HealthStatus.FAIL,
            entity_failures={
                0: dcgm.types.ErrorDetails(
                    code="DCGM_FR_CORRUPT_INFOROM",
                    message="A corrupt InfoROM has been detected in GPU 0. Flash the InfoROM to clear this corruption.",
                )
            },
        )
        fatal_dcgm_health_events_length = 0
        for watch_name in dcgm_health_events.keys():
            if dcgm_health_conditions_categorization_mapping_config[watch_name] == "Fatal":
                fatal_dcgm_health_events_length += 1

        gpu_ids = [0, 1, 2, 3, 4, 5, 6, 7]
        platform_connector_test.health_event_occurred(dcgm_health_events, gpu_ids, gpu_serials)
        health_events = healthEventProcessor.health_events
        for event in health_events:
            if event.checkName == "GpuInforomWatch" and event.isHealthy == False:
                assert event.errorCode[0] == "DCGM_FR_CORRUPT_INFOROM"
                assert event.entitiesImpacted[0].entityValue == "0"
                assert event.recommendedAction == platformconnector_pb2.RecommendedAction.COMPONENT_RESET
            else:
                assert event.isHealthy == True
                assert event.checkName != ""
                assert fatal_dcgm_health_events_length * len(gpu_ids) == len(health_events)

        # check if cache is not updated with change no in event
        dcgm_health_events["DCGM_HEALTH_WATCH_INFOROM"] = dcgmtypes.HealthDetails(
            status=dcgmtypes.HealthStatus.FAIL,
            entity_failures={
                0: dcgm.types.ErrorDetails(
                    code="DCGM_FR_CORRUPT_INFOROM",
                    message="A corrupt InfoROM has been detected in GPU 0. Flash the InfoROM to clear this corruption.",
                )
            },
        )

        check_name = platform_connector_test._convert_dcgm_watch_name_to_check_name("DCGM_HEALTH_WATCH_INFOROM")
        dcgm_health_event_key = platform_connector_test._build_cache_key(
            check_name,
            "GPU",
            "0",
        )
        before_insertion_cache_value = platform_connector_test.entity_cache[dcgm_health_event_key]
        cache_length = len(platform_connector_test.entity_cache)
        platform_connector_test.health_event_occurred(dcgm_health_events, gpu_ids, gpu_serials)
        health_events = healthEventProcessor.health_events
        assert len(platform_connector_test.entity_cache) == cache_length
        assert (
            platform_connector_test.entity_cache[dcgm_health_event_key].isFatal == before_insertion_cache_value.isFatal
        )
        assert (
            platform_connector_test.entity_cache[dcgm_health_event_key].isHealthy
            == before_insertion_cache_value.isHealthy
        )

        # check if cache is updated with change in event
        dcgm_health_events["DCGM_HEALTH_WATCH_INFOROM"] = dcgmtypes.HealthDetails(
            status=dcgmtypes.HealthStatus.PASS, entity_failures={}
        )

        check_name = platform_connector_test._convert_dcgm_watch_name_to_check_name("DCGM_HEALTH_WATCH_INFOROM")
        cache_length = len(platform_connector_test.entity_cache)
        platform_connector_test.health_event_occurred(dcgm_health_events, gpu_ids, gpu_serials)

        # Verify healthy event was added to cache with correct message format
        dcgm_health_event_key = platform_connector_test._build_cache_key(
            check_name,
            "GPU",
            "0",
        )
        assert dcgm_health_event_key in platform_connector_test.entity_cache
        assert platform_connector_test.entity_cache[dcgm_health_event_key].isFatal == False
        assert platform_connector_test.entity_cache[dcgm_health_event_key].isHealthy == True

        server.stop(0)

    def test_health_event_multiple_failures_same_gpu(self):
        """Test that multiple NvLink failures for the same GPU are properly published to gRPC."""
        healthEventProcessor = PlatformConnectorServicer()
        server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))
        platformconnector_pb2_grpc.add_PlatformConnectorServicer_to_server(healthEventProcessor, server)
        server.add_insecure_port(f"unix://{socket_path}")
        server.start()

        watcher = dcgm.DCGMWatcher(
            addr="localhost:5555",
            poll_interval_seconds=10,
            callbacks=[],
            dcgm_k8s_service_enabled=False,
        )

        gpu_serials = {
            0: "1650924060039",
            1: "1650924060040",
            2: "1650924060041",
            3: "1650924060042",
            4: "1650924060043",
            5: "1650924060044",
            6: "1650924060045",
            7: "1650924060046",
        }

        exit = Event()
        dcgm_errors_info_dict = {}
        dcgm_errors_info_dict["DCGM_FR_NVLINK_DOWN"] = "COMPONENT_RESET"

        dcgm_health_conditions_categorization_mapping_config = {}
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_NVLINK"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_PCIE"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_MEM"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_INFOROM"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_MCU"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_DRIVER"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_NVSWITCH_FATAL"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_NVSWITCH_NONFATAL"] = "NonFatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_SM"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_THERMAL"] = "NonFatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_POWER"] = "NonFatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_PMU"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_CPUSET"] = "NonFatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_NVSWITCH"] = "Fatal"

        platform_connector_test = platform_connector.PlatformConnectorEventProcessor(
            socket_path,
            node_name,
            exit,
            dcgm_errors_info_dict,
            "statefile",
            dcgm_health_conditions_categorization_mapping_config,
        )

        # Simulate multiple NvLink failures for GPU 0 (4 links down: 8, 9, 14, 15)
        # This mimics the aggregated message from dcgm.py after the fix
        dcgm_health_events = watcher._get_health_status_dict()
        aggregated_message = (
            "GPU 0's NvLink link 8 is currently down Check DCGM and system logs for errors. Reset GPU. Restart DCGM. Rerun diagnostics.; "
            "GPU 0's NvLink link 9 is currently down Check DCGM and system logs for errors. Reset GPU. Restart DCGM. Rerun diagnostics.; "
            "GPU 0's NvLink link 14 is currently down Check DCGM and system logs for errors. Reset GPU. Restart DCGM. Rerun diagnostics.; "
            "GPU 0's NvLink link 15 is currently down Check DCGM and system logs for errors. Reset GPU. Restart DCGM. Rerun diagnostics."
        )

        dcgm_health_events["DCGM_HEALTH_WATCH_NVLINK"] = dcgmtypes.HealthDetails(
            status=dcgmtypes.HealthStatus.FAIL,
            entity_failures={
                0: dcgm.types.ErrorDetails(
                    code="DCGM_FR_NVLINK_DOWN",
                    message=aggregated_message,
                )
            },
        )

        gpu_ids = [0, 1, 2, 3, 4, 5, 6, 7]
        platform_connector_test.health_event_occurred(dcgm_health_events, gpu_ids, gpu_serials)

        health_events = healthEventProcessor.health_events

        # Find the NvLink failure event for GPU 0
        nvlink_failure_event = None
        for event in health_events:
            if (
                event.checkName == "GpuNvlinkWatch"
                and not event.isHealthy
                and event.entitiesImpacted[0].entityValue == "0"
            ):
                nvlink_failure_event = event
                break

        # Verify the event was published
        assert nvlink_failure_event is not None, "NvLink failure event for GPU 0 not found"
        assert nvlink_failure_event.errorCode[0] == "DCGM_FR_NVLINK_DOWN"
        assert nvlink_failure_event.isFatal == True
        assert nvlink_failure_event.isHealthy == False
        assert nvlink_failure_event.entitiesImpacted[0].entityValue == "0"
        assert nvlink_failure_event.recommendedAction == platformconnector_pb2.RecommendedAction.COMPONENT_RESET
        assert nvlink_failure_event.metadata["SerialNumber"] == "1650924060039"

        # Verify all 4 NvLink failures are in the message
        assert "link 8" in nvlink_failure_event.message, "Link 8 failure missing from message"
        assert "link 9" in nvlink_failure_event.message, "Link 9 failure missing from message"
        assert "link 14" in nvlink_failure_event.message, "Link 14 failure missing from message"
        assert "link 15" in nvlink_failure_event.message, "Link 15 failure missing from message"

        # Verify messages are properly separated
        assert nvlink_failure_event.message.count(";") == 3, "Expected 3 semicolons separating 4 messages"

        # Verify the complete aggregated message is preserved
        assert nvlink_failure_event.message == aggregated_message

        server.stop(0)

    def test_health_event_multiple_gpus_multiple_failures_each(self):
        """Test that multiple NvLink failures across multiple GPUs are properly published to gRPC."""
        healthEventProcessor = PlatformConnectorServicer()
        server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))
        platformconnector_pb2_grpc.add_PlatformConnectorServicer_to_server(healthEventProcessor, server)
        server.add_insecure_port(f"unix://{socket_path}")
        server.start()

        watcher = dcgm.DCGMWatcher(
            addr="localhost:5555",
            poll_interval_seconds=10,
            callbacks=[],
            dcgm_k8s_service_enabled=False,
        )

        gpu_serials = {
            0: "1650924060039",
            1: "1650924060040",
            2: "1650924060041",
            3: "1650924060042",
            4: "1650924060043",
            5: "1650924060044",
            6: "1650924060045",
            7: "1650924060046",
        }

        exit = Event()
        dcgm_errors_info_dict = {}
        dcgm_errors_info_dict["DCGM_FR_NVLINK_DOWN"] = "COMPONENT_RESET"

        dcgm_health_conditions_categorization_mapping_config = {}
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_NVLINK"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_PCIE"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_MEM"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_INFOROM"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_MCU"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_DRIVER"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_NVSWITCH_FATAL"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_NVSWITCH_NONFATAL"] = "NonFatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_SM"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_THERMAL"] = "NonFatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_POWER"] = "NonFatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_PMU"] = "Fatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_CPUSET"] = "NonFatal"
        dcgm_health_conditions_categorization_mapping_config["DCGM_HEALTH_WATCH_NVSWITCH"] = "Fatal"

        platform_connector_test = platform_connector.PlatformConnectorEventProcessor(
            socket_path,
            node_name,
            exit,
            dcgm_errors_info_dict,
            "statefile",
            dcgm_health_conditions_categorization_mapping_config,
        )

        # Simulate multiple NvLink failures for GPU 0 and GPU 1
        dcgm_health_events = watcher._get_health_status_dict()

        gpu0_message = (
            "GPU 0's NvLink link 8 is currently down Check DCGM and system logs for errors. Reset GPU. Restart DCGM. Rerun diagnostics.; "
            "GPU 0's NvLink link 9 is currently down Check DCGM and system logs for errors. Reset GPU. Restart DCGM. Rerun diagnostics.; "
            "GPU 0's NvLink link 14 is currently down Check DCGM and system logs for errors. Reset GPU. Restart DCGM. Rerun diagnostics.; "
            "GPU 0's NvLink link 15 is currently down Check DCGM and system logs for errors. Reset GPU. Restart DCGM. Rerun diagnostics."
        )

        gpu1_message = (
            "GPU 1's NvLink link 8 is currently down Check DCGM and system logs for errors. Reset GPU. Restart DCGM. Rerun diagnostics.; "
            "GPU 1's NvLink link 9 is currently down Check DCGM and system logs for errors. Reset GPU. Restart DCGM. Rerun diagnostics.; "
            "GPU 1's NvLink link 12 is currently down Check DCGM and system logs for errors. Reset GPU. Restart DCGM. Rerun diagnostics.; "
            "GPU 1's NvLink link 13 is currently down Check DCGM and system logs for errors. Reset GPU. Restart DCGM. Rerun diagnostics."
        )

        dcgm_health_events["DCGM_HEALTH_WATCH_NVLINK"] = dcgmtypes.HealthDetails(
            status=dcgmtypes.HealthStatus.FAIL,
            entity_failures={
                0: dcgm.types.ErrorDetails(
                    code="DCGM_FR_NVLINK_DOWN",
                    message=gpu0_message,
                ),
                1: dcgm.types.ErrorDetails(
                    code="DCGM_FR_NVLINK_DOWN",
                    message=gpu1_message,
                ),
            },
        )

        gpu_ids = [0, 1, 2, 3, 4, 5, 6, 7]
        platform_connector_test.health_event_occurred(dcgm_health_events, gpu_ids, gpu_serials)

        health_events = healthEventProcessor.health_events

        # Find NvLink failure events for both GPUs
        gpu0_event = None
        gpu1_event = None
        for event in health_events:
            if event.checkName == "GpuNvlinkWatch" and not event.isHealthy:
                if event.entitiesImpacted[0].entityValue == "0":
                    gpu0_event = event
                elif event.entitiesImpacted[0].entityValue == "1":
                    gpu1_event = event

        # Verify GPU 0 event
        assert gpu0_event is not None, "NvLink failure event for GPU 0 not found"
        assert gpu0_event.errorCode[0] == "DCGM_FR_NVLINK_DOWN"
        assert gpu0_event.isFatal == True
        assert gpu0_event.isHealthy == False
        assert gpu0_event.metadata["SerialNumber"] == "1650924060039"

        # Verify all 4 NvLink failures for GPU 0
        assert "link 8" in gpu0_event.message
        assert "link 9" in gpu0_event.message
        assert "link 14" in gpu0_event.message
        assert "link 15" in gpu0_event.message
        assert gpu0_event.message.count(";") == 3
        assert gpu0_event.message == gpu0_message

        # Verify GPU 1 event
        assert gpu1_event is not None, "NvLink failure event for GPU 1 not found"
        assert gpu1_event.errorCode[0] == "DCGM_FR_NVLINK_DOWN"
        assert gpu1_event.isFatal == True
        assert gpu1_event.isHealthy == False
        assert gpu1_event.metadata["SerialNumber"] == "1650924060040"

        # Verify all 4 NvLink failures for GPU 1
        assert "link 8" in gpu1_event.message
        assert "link 9" in gpu1_event.message
        assert "link 12" in gpu1_event.message
        assert "link 13" in gpu1_event.message
        assert gpu1_event.message.count(";") == 3
        assert gpu1_event.message == gpu1_message

        server.stop(0)

    def test_dcgm_connectivity_failed(self):
        """Test that GpuDcgmConnectivityFailure health event is sent when DCGM connectivity fails."""
        import tempfile
        import os

        # Create a temporary state file with test boot ID
        with tempfile.NamedTemporaryFile(mode="w", delete=False, suffix="_test_state") as f:
            f.write("test_boot_id")
            state_file_path = f.name

        try:
            healthEventProcessor = PlatformConnectorServicer()
            server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))
            platformconnector_pb2_grpc.add_PlatformConnectorServicer_to_server(healthEventProcessor, server)
            server.add_insecure_port(f"unix://{socket_path}")
            server.start()

            exit = Event()

            dcgm_errors_info_dict = {}
            dcgm_health_conditions_categorization_mapping_config = {
                "DCGM_HEALTH_WATCH_PCIE": "Fatal",
                "DCGM_HEALTH_WATCH_NVLINK": "Fatal",
            }

            platform_connector_processor = platform_connector.PlatformConnectorEventProcessor(
                socket_path=socket_path,
                node_name=node_name,
                exit=exit,
                dcgm_errors_info_dict=dcgm_errors_info_dict,
                state_file_path=state_file_path,
                dcgm_health_conditions_categorization_mapping_config=dcgm_health_conditions_categorization_mapping_config,
            )

            # Trigger connectivity failure
            platform_connector_processor.dcgm_connectivity_failed()
            time.sleep(1)  # Allow time for event to be sent

            # Verify the health events
            health_events = healthEventProcessor.health_events
            assert len(health_events) == 1

            for i, event in enumerate(health_events):
                assert event.checkName == "GpuDcgmConnectivityFailure"
                assert event.isFatal == True
                assert event.isHealthy == False
                assert event.errorCode == ["DCGM_CONNECTIVITY_ERROR"]
                assert event.message == "Failed to connect to DCGM for health check"
                assert event.recommendedAction == platformconnector_pb2.CONTACT_SUPPORT
                assert event.nodeName == node_name
                assert event.entitiesImpacted == []
                assert event.metadata["SerialNumber"] == ""

            server.stop(0)
        finally:
            # Clean up the temporary state file
            if os.path.exists(state_file_path):
                os.unlink(state_file_path)

    def test_dcgm_connectivity_restored(self):
        """Test that connectivity restored event is sent when DCGM connectivity is restored."""
        healthEventProcessor = PlatformConnectorServicer()
        server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))
        platformconnector_pb2_grpc.add_PlatformConnectorServicer_to_server(healthEventProcessor, server)
        server.add_insecure_port(f"unix://{socket_path}")
        server.start()
        exit = Event()

        dcgm_errors_info_dict = {}
        dcgm_health_conditions_categorization_mapping_config = {
            "DCGM_HEALTH_WATCH_PCIE": "Fatal",
        }

        platform_connector_processor = platform_connector.PlatformConnectorEventProcessor(
            socket_path=socket_path,
            node_name=node_name,
            exit=exit,
            dcgm_errors_info_dict=dcgm_errors_info_dict,
            state_file_path="statefile",
            dcgm_health_conditions_categorization_mapping_config=dcgm_health_conditions_categorization_mapping_config,
        )

        timestamp = Timestamp()
        timestamp.GetCurrentTime()
        platform_connector_processor.clear_dcgm_connectivity_failure(timestamp)
        time.sleep(1)

        # The events should include connectivity restored
        health_events = healthEventProcessor.health_events

        # Find the connectivity restored event
        restored_event = None
        for event in health_events:
            if event.checkName == "GpuDcgmConnectivityFailure" and event.isHealthy == True:
                restored_event = event
                break

        assert (
            restored_event is not None
        ), f"No restored event found. Events: {[f'{e.checkName}:{e.isHealthy}' for e in health_events]}"
        assert restored_event.isFatal == False
        assert restored_event.isHealthy == True
        assert restored_event.errorCode == []
        assert restored_event.message == "DCGM connectivity reported no errors"
        assert restored_event.recommendedAction == platformconnector_pb2.NONE

        server.stop(0)
