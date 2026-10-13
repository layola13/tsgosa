function selectionSort(a: i32[]): i32[] {
  const n: i32 = a.length;
  for (let i: i32 = 0; i < n; i++) {
    let m: i32 = i;
    for (let j: i32 = i + 1; j < n; j++) {
      if (a[j] < a[m]) {
        m = j;
      }
    }
    [a[i], a[m]] = [a[m], a[i]];
  }
  return a;
}
function main(): i32 {
  const a: i32[] = [3, 1, 2];
  const s: i32[] = selectionSort(a);
  console.log(s[0]);
  console.log(s[1]);
  console.log(s[2]);
  return 0;
}
