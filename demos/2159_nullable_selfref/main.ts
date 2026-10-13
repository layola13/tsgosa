class Node {
  v: number;
  next: Node;
  constructor(v: number, next: Node) { this.v = v; this.next = next; }
}
function main(): i32 {
  const n2 = new Node(2, null as any);
  const n1 = new Node(1, n2);
  const nx = n1.next;
  console.log(nx.v);
  return 0;
}
