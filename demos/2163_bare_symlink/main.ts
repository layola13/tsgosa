import { bubbleSort } from 'talgsrc/sorts/bubble_sort'
function main(): i32 {
  const a: i32[] = [3, 1, 2];
  const s: i32[] = bubbleSort(a);
  console.log(s[0]);
  console.log(s[1]);
  console.log(s[2]);
  return 0;
}
console.log(main())
