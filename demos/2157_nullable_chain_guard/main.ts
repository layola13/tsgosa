class Node {
  v: number;
  next: Node | null;
  constructor(v: number, next: Node | null) { this.v = v; this.next = next; }
}
function main(): i32 {
  const n2 = new Node(2, null);
  const n1 = new Node(1, n2);
  const nx = n1.next;
  console.log(nx?.v ?? -1);
  console.log(n1.next?.v ?? -1);
  return 0;
}
