function bsearch(a: number[], key: i32): i32 {
  for (let i: i32 = 0; i < a.length; i++) {
    if (a[i] == key) {
      return i;
    }
  }
  return -1;
}
function main(): i32 {
  const a: number[] = [1, 3, 5, 7, 9];
  console.log(bsearch(a, 5), bsearch(a, 4));
  return 0;
}
