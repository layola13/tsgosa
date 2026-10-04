function main(): i32 {
  const a: number[] = [1, 3, 2, 3, 1, 3];
  let best: i32 = a[0];
  let bestN: i32 = 0;
  for (let i: i32 = 0; i < a.length; i++) {
    let n: i32 = 0;
    for (let j: i32 = 0; j < a.length; j++) {
      if (a[j] == a[i]) {
        n = n + 1;
      }
    }
    if (n > bestN) {
      bestN = n;
      best = a[i];
    }
  }
  console.log(best, bestN);
  return 0;
}