interface Q {
  a: i32[];
}
function sum(a: i32[]): i32 {
  return a[0];
}
function main(): i32 {
  const q: Q | null = null;
  console.log(sum(q.a));
  return 0;
}
