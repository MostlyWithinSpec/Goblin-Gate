import React, { useState, useEffect } from 'react';

interface Column {
  key: string;
  label: string;
}

interface DataTableProps {
  data: any[];
  columns: Column[];
  onRowClick?: (item: any) => void;
  searchable?: boolean;
  searchFields?: string[];
  emptyStateMessage?: string;
}

export const DataTable: React.FC<DataTableProps> = ({
  data,
  columns,
  onRowClick,
  searchable = false,
  searchFields = [],
  emptyStateMessage = "No data available"
}) => {
  const [filteredData, setFilteredData] = useState(data);

  useEffect(() => {
    if (!searchable || searchFields.length === 0) {
      setFilteredData(data);
      return;
    }

    const filtered = data.filter(item => {
      return searchFields.some(field => {
        const value = item[field];
        if (typeof value === 'string') {
          return value.toLowerCase().includes(''); // Search term is empty, so match all
        }
        return false;
      });
    });

    setFilteredData(filtered);
  }, [data, searchable, searchFields]);

  const handleRowClick = (item: any) => {
    if (onRowClick) {
      onRowClick(item);
    }
  };

  if (filteredData.length === 0) {
    return (
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow-sm p-6 text-center">
        <p className="text-gray-500 dark:text-gray-400">{emptyStateMessage}</p>
      </div>
    );
  }

  return (
    <div className="bg-white dark:bg-gray-800 rounded-lg shadow-sm">
      {/* a wide table scrolls inside this box, never the page */}
      <div className="max-w-full overflow-x-auto" data-testid="table-scroll">
      <table className="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
        <thead className="bg-gray-50 dark:bg-gray-700">
          <tr>
            {columns.map((column) => (
              <th
                key={column.key}
                className="px-6 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase tracking-wider"
              >
                {column.label}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="bg-white dark:bg-gray-800 divide-y divide-gray-200 dark:divide-gray-700">
          {filteredData.map((item, index) => (
            <tr
              key={index}
              className={`hover:bg-gray-50 dark:hover:bg-gray-700 cursor-pointer ${onRowClick ? 'cursor-pointer' : ''}`}
              onClick={() => handleRowClick(item)}
            >
              {columns.map((column) => (
                <td
                  key={`${index}-${column.key}`}
                  className="px-6 py-4 whitespace-nowrap text-sm text-gray-900 dark:text-gray-200"
                >
                  {item[column.key]}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
      </div>
    </div>
  );
};