import { createBrowserRouter } from 'react-router-dom';
import { AppShell } from './AppShell';
import { SearchPage } from '../pages/SearchPage';
import { AskPage } from '../pages/AskPage';
import { OpsPage } from '../pages/OpsPage';

export const router = createBrowserRouter([
  {
    path: '/',
    element: <AppShell />,
    children: [
      { index: true, element: <SearchPage /> },
      { path: 'ask', element: <AskPage /> },
      { path: 'ops', element: <OpsPage /> },
    ],
  },
]);
