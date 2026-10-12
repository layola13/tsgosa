class Node {
  v: number;
  next: Node | null;
  constructor(v: number, next: Node | null) { this.v = v; this.next = next; }
  val(): number { return this.v; }
}
function main(): i32 {
  const n2 = new Node(2, null);
  const n1 = new Node(1, n2);
  const nx = n1.next;
  console.log(n1.v + nx.v);
  return 0;
}
