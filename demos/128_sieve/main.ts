function main(): i32 {
  const n: i32 = 20;
  const isP: number[] = [];
  for (let i: i32 = 0; i < n; i++) {
    isP.push(1);
  }
  for (let i: i32 = 2; i < n; i++) {
    if (isP[i] == 1) {
      for (let j: i32 = i + i; j < n; j = j + i) {
        isP[j] = 0;
      }
    }
  }
  let c: i32 = 0;
  for (let i: i32 = 2; i < n; i++) {
    c = c + isP[i];
  }
  console.log(c, isP[17]);
  return 0;
}
