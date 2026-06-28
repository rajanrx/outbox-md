import { useCallback, useEffect, useState } from "react";
import { getDoc, type Comment } from "../api";

export function useComments(docId: string) {
  const [comments, setComments] = useState<Comment[]>([]);
  const refresh = useCallback(async () => {
    if (!docId) return;
    const v = await getDoc(docId);
    setComments(v.comments ?? []);
  }, [docId]);
  useEffect(() => { refresh(); }, [refresh]);
  return { comments, refresh };
}
