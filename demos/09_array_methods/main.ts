function main(): i32 {
  const a: number[] = [1, 2, 3];
  a.push(4);
  console.log(a.length);
  const sq = a.map((x) => x * x);
  console.log(sq[3]);
  const ev = a.filter((x) => x % 2 == 0);
  console.log(ev.length, ev[0]);
  let t: i32 = 0;
  a.forEach((x) => {
    t = t + x;
  });
  console.log(t);
  return 0;
}
