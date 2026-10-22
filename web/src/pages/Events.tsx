import LiveEventsCard from '../components/LiveEventsCard'

// Events is the live event stream of the box on its own page (task 103),
// reading GET /api/v1/events over Server-Sent Events.
export default function Events() {
  return (
    <div className="p-6">
      <h1 className="mb-4 text-2xl font-bold text-gray-800 dark:text-white">Live events</h1>
      <div className="max-w-3xl">
        <LiveEventsCard />
      </div>
    </div>
  )
}
