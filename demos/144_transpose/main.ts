function main(): i32 {
  const m: number[][] = [[1, 2], [3, 4]];
  const t: number[][] = [[m[0][0], m[1][0]], [m[0][1], m[1][1]]];
  console.log(t[0][0], t[0][1], t[1][0], t[1][1]);
  return 0;
}