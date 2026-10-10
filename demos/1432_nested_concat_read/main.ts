function main(): i32 {
  const p: number[][] = [[1, 2]];
  const q: number[][] = p.concat([[3]]);
  console.log(q.length);
  console.log(q[0][0]);
  console.log(q[1][0]);
  return 0;
}
