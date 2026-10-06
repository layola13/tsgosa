function main(): number {
  let i: f64 = 0;
  let n = 0;
  for (i = 0; i < 3; i = i + 1) {
    n = n + 1;
  }
  let m = 0;
  for (i = 0; i < 3.5; i = i + 0.5) {
    m = m + 1;
  }
  console.log(n);
  console.log(m);
  return n + m * 10;
}
console.log(main());
