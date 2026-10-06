function main(): i32 {
  const t: [i32, i32] = [1, 2];
  const u: [x: i32, y: i32] = [5, 6];
  const [a, b] = t;
  let w: [i32, i32];
  w = [7, 8];
  console.log(t[0] + t[1] + u[0] + u[1] + a + b + w[0] + w[1]);
  return t[0] + t[1] + u[0] + u[1] + a + b + w[0] + w[1];
}
console.log(main());
